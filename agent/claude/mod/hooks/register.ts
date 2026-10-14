import type { EngineInterface, On } from 'claude-code'

type Args = Record<string, unknown>

type State = {
  active: boolean
  cwd: string
  sessionId: string
  // bin is the oriel binary every decision is handed to, and sock the session's socket.
  bin: string
  sock: string
  working: boolean
  turnId: string
  // Oriel's tools, by full name (mcp__oriel__echo) to the name Oriel knows them by.
  tools: Map<string, string>
  subagents: Set<string>
}

// clip bounds what an event carries: Oriel shows it, it doesn't need it whole.
const clip = (s: string, max = 20_000) => (s && s.length > max ? s.slice(0, max) + '\n…' : s ?? '')

// host runs `oriel hook <op>` with body on stdin, and answers with its parsed output, or undefined when it failed.
async function host($: EngineInterface, st: State, op: string, body: unknown, timeoutMs = 30_000) {
  try {
    const r = await $.process.run([st.bin, 'hook', op], {
      cwd: st.cwd,
      env: { ORIEL_HOOK_SOCK: st.sock },
      stdin: JSON.stringify(body ?? {}),
      timeoutMs,
    })
    return r.exitCode === 0 ? (JSON.parse(r.stdout || '{}') as Args) : undefined
  } catch {
    return undefined
  }
}

const event = ($: EngineInterface, st: State, body: Args) => host($, st, 'event', body)

async function abortTurn($: EngineInterface, st: State) {
  if (st.working && st.turnId) await $.turn.abort({ turnId: st.turnId }).catch(() => undefined)
}

// A side question, answered from a fork of the session: tool-less, and outside the main conversation.
async function fork($: EngineInterface, st: State, id: string, prompt: string) {
  let answer: Args
  try {
    const r = await $.model.fork({ prompt })
    answer = r.isAnswered ? { id, answered: true, text: r.text } : { id, answered: false, reason: r.reason }
  } catch (err) {
    answer = { id, answered: false, reason: err instanceof Error ? err.message : String(err) }
  }
  await host($, st, 'fork-answer', answer)
}

// poll long-polls Oriel for what to do next: a prompt to submit, a turn to abort, a question to fork.
async function poll($: EngineInterface, st: State) {
  for (;;) {
    const out = await host($, st, 'wait', {}, 90_000)
    if (!out) {
      await $.clock.sleep(5_000)
      continue
    }
    switch (out.kind) {
      case 'submit':
        if (out.urgent) await abortTurn($, st)
        void $.prompt.submit({ text: String(out.text ?? ''), asUser: true }).catch(() => undefined)
        break
      case 'abort':
        await abortTurn($, st)
        break
      case 'fork':
        void fork($, st, String(out.id), String(out.prompt ?? ''))
        break
      case 'usage':
        void reportUsage($, st)
        break
    }
  }
}

// The plan's windows as Claude Code holds them now: none until a request or /usage has read them.
async function reportUsage($: EngineInterface, st: State) {
  const u = await $.session.usage().catch(() => undefined)
  await event($, st, { kind: 'usage', rateLimits: u?.rateLimits ?? [] })
}

async function reportTurnEnd($: EngineInterface, st: State, e: Args) {
  const u = await $.session.usage().catch(() => undefined)
  await event($, st, {
    kind: 'turn-complete',
    turnId: e.turnId,
    reason: e.reason,
    answer: clip(String(e.answer ?? '')),
    durationMs: e.durationMs,
    usage: e.usage,
    context: u?.context ? { tokens: u.context.tokens, window: u.context.window } : undefined,
    cost: u?.cost?.usd,
    rateLimits: u?.rateLimits ?? [],
  })
}

// passOn leaves a call whose hook failed to the hooks beneath and the engine, as if this mod weren't here.
const passOn = ($: any, e: any, next: any) => next(e)

export function register(on: On): void {
  const st: State = {
    active: false,
    cwd: '',
    sessionId: '',
    bin: 'oriel',
    sock: '',
    working: false,
    turnId: '',
    tools: new Map(),
    subagents: new Set(),
  }

  on('session.start', async ($, e, next) => {
    const r = await next(e)
    if ((await $.env.get('ORIEL_SPAWNED')) !== '1' || st.active) return r
    st.active = true
    st.cwd = e.cwd
    st.sessionId = await $.session.id()
    st.bin = (await $.env.get('ORIEL_BIN')) || 'oriel'
    st.sock = (await $.env.get('ORIEL_HOOK_SOCK')) || ''
    const out = await host($, st, 'hello', { sessionId: st.sessionId, cwd: st.cwd })
    const tools = Array.isArray(out?.tools) ? (out.tools as Args[]) : []
    for (const t of tools) {
      const name = String(t.name)
      const reg = await $.tool
        .register({
          name,
          description: String(t.description ?? ''),
          inputSchema: (t.inputSchema as Record<string, unknown>) ?? { type: 'object' },
        })
        .catch(() => undefined)
      st.tools.set(reg?.tool ?? `mcp__oriel__${name}`, name)
    }
    void poll($, st)
    return r
  })

  on('session.end', async ($, e, next) => {
    if (st.active) await event($, st, { kind: 'end', reason: e.reason })
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    st.turnId = e.turnId
    if (st.active && !st.working) {
      st.working = true
      await event($, st, { kind: 'turn-start', turnId: e.turnId })
    }
    return next(e)
  })

  on('agent.spawn', async ($, e, next) => {
    const r = await next(e)
    if (st.active && r.agentId) {
      st.subagents.add(r.agentId)
      void event($, st, {
        kind: 'subagent-start',
        id: r.agentId,
        description: e.description,
        type: e.subagentType,
        prompt: clip(e.prompt),
      })
    }
    return r
  }).catch(passOn)

  on('turn.step', async function* ($, e, next) {
    if (st.active && e.agentId && !st.subagents.has(e.agentId)) {
      st.subagents.add(e.agentId)
      void event($, st, { kind: 'subagent-run', id: e.agentId })
    }
    return yield* next(e)
  })

  on('turn.complete', async ($, e, next) => {
    const r = await next(e)
    if (!st.active) return r
    // A subagent's run ending says nothing about the main loop's turn.
    if (e.agentId) {
      st.subagents.delete(e.agentId)
      void event($, st, { kind: 'subagent-end', id: e.agentId, reason: e.reason, text: clip(e.answer) })
      return r
    }
    st.working = false
    void reportTurnEnd($, st, e as unknown as Args)
    return r
  })

  // A turn the API ended once Claude Code's own retries ran out.
  on('classic.StopFailure', async ($, e, next) => {
    if (st.active) void event($, st, { kind: 'api-error', error: e.error, text: e.error_details ?? e.last_assistant_message ?? '' })
    return next(e)
  }).catch(passOn)

  // The guard rails are Oriel's: it allows, denies, or leaves the call to the mode and its dialog.
  on('tool.check', async ($, e, next) => {
    if (!st.active) return next(e)
    const out = await host($, st, 'check', { tool: e.tool, input: e.input, agentId: e.agentId })
    if (out?.decision === 'allow' || out?.decision === 'deny') {
      return { decision: out.decision, reason: out.reason ? String(out.reason) : undefined }
    }
    return next(e)
  }).catch(passOn)

  // Fires only when the engine is about to open its dialog. Oriel answers it instead; with no answer from Oriel
  // the dialog opens as usual.
  let permSeq = 0
  on('classic.PermissionRequest', async ($, e, next) => {
    const r = await next(e)
    if (!st.active || r.decision || e.tool_name === 'AskUserQuestion') return r
    const id = `${Date.now()}-${++permSeq}`
    const out = await host(
      $,
      st,
      'permission',
      { id, tool: e.tool_name, input: e.tool_input, suggestions: e.permission_suggestions },
      600_000,
    )
    if (out?.decision === 'allow') {
      return {
        ...r,
        decision: { behavior: 'allow', updatedPermissions: out.always ? e.permission_suggestions : undefined },
      }
    }
    if (out?.decision === 'deny') return { ...r, decision: { behavior: 'deny', message: String(out.message ?? '') } }
    return r
  }).catch(passOn)

  on('tool.call', { tool: 'AskUserQuestion' }, async ($, e, next) => {
    if (!st.active) return next(e)
    const questions = (e as unknown as Args).questions
    const out = await host($, st, 'question', { id: e.tool_use_id, input: { questions } }, 600_000)
    if (out?.deny) return { deny: String(out.deny) }
    if (!out?.answers) return next(e)
    return { result: { questions, answers: out.answers } } as never
  }).catch(passOn)

  // Oriel's own tools run on the host.
  on('tool.call', async ($, e, next) => {
    const name = st.tools.get(e.tool)
    if (!st.active || name === undefined) return next(e)
    const { tool: _tool, tool_use_id, agentId: _agent, requestMeta: _meta, ...input } = e as unknown as Args
    const out = await host($, st, 'tool', { name, id: tool_use_id, input }, 600_000)
    if (!out) return { isError: true, result: `Oriel did not answer the ${name} call.` }
    return out.isError ? { isError: true, result: String(out.result ?? '') } : { result: String(out.result ?? '') }
  }).catch(passOn)
}
