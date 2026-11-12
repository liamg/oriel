import type { Args as EventArgs, EngineInterface, EventResult, On } from 'claude-code'

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
  // background is the work the turn ending left running, from its Stop hook: it wakes the session again.
  background: unknown[]
  // servers are Oriel's MCP servers the session has had (oriel-gw, from mcp__oriel-gw__…).
  servers: Set<string>
  // unattended: nobody is at this terminal, so a dialog would wait forever; what Oriel leaves unanswered is refused.
  unattended: boolean
  // mode is the permission mode Oriel was last told of: a restart resumes the conversation in it.
  mode: string
  // pending: a call went to a person, and Oriel's PermissionRequest hook hasn't been reached for it yet.
  pending: boolean
  // skipped: Claude Code's built-in sec-default plugin continues past Oriel's PermissionRequest hook (store SKIPPED), so only the engine's
  // dialog in the terminal answers; Oriel is told of each such dialog from tool.check.
  skipped: boolean
  // dialog: Oriel was told the engine's own dialog asks about a call, and hasn't been told it's gone.
  dialog: boolean
}

// NOBODY is what an unattended agent is told of an ask Oriel didn't answer: agent.UnattendedRefusal.
const NOBODY = 'Nobody is here to answer this: carry on without it, or say in your reply what the user should do.'

// ORIEL marks an ask of Oriel's own from check: a person's in any mode. The engine pins its own name over it.
const ORIEL = 'oriel'

// SKIPPED is the store's key for State.skipped: it holds across sessions, and a reached PermissionRequest hook clears it.
const SKIPPED = 'permissionRequestSkipped'

// CHECK_FAILED is why an MCP call is refused when Oriel didn't answer its check: claude.CheckFailed.
const CHECK_FAILED = "Oriel couldn't be asked about this call, so it was refused."

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

// noteMode tells Oriel the session's permission mode when it's changed (Shift+Tab, plan mode's exit), as a classic
// hook's permission_mode reads it; nothing says so as it changes.
async function noteMode($: EngineInterface, st: State, mode: string | undefined) {
  if (!st.active || !mode || mode === st.mode) return
  st.mode = mode
  await event($, st, { kind: 'mode', mode })
}

// check is Oriel's verdict on a call: it allows, denies, asks, or leaves the call to the mode and its dialog.
async function check($: EngineInterface, st: State, e: EventArgs<'tool.check'>, next: (e: EventArgs<'tool.check'>) => Promise<EventResult<'tool.check'>>) {
  const out = await host($, st, 'check', { tool: e.tool, input: e.input, agentId: e.agentId })
  // No answer from Oriel: its own tools still run, and an MCP call is refused rather than left to a mode that
  // might let it through unasked (the gateway's holds its own calls).
  if (!out) {
    if (st.tools.has(e.tool) || e.tool.startsWith('mcp__oriel__')) return { decision: 'allow' }
    if (e.tool.startsWith('mcp__') && !e.tool.startsWith('mcp__oriel-gw__')) return { decision: 'deny', reason: CHECK_FAILED }
    return next(e)
  }
  const reason = out?.reason ? String(out.reason) : undefined
  if (out?.decision === 'allow' || out?.decision === 'deny') return { decision: out.decision, reason }
  if (out?.decision === 'ask') {
    const core = await next(e)
    if (core.decision === 'deny' || (core.decision === 'allow' && core.rule)) return core
    return { decision: 'ask', reason, plugin: ORIEL }
  }
  return next(e)
}

// dialogGone tells Oriel the engine's dialog it was told of is answered: the call ran, or another came.
async function dialogGone($: EngineInterface, st: State) {
  if (!st.dialog) return
  st.dialog = false
  await event($, st, { kind: 'dialog-gone' })
}

// settle is the last call a person was asked about moving on (it ran, or another call or the turn's end came) with
// no PermissionRequest reaching Oriel: a plugin above continued past its hook, and the engine's dialog answered.
async function settle($: EngineInterface, st: State) {
  if (st.pending) {
    st.pending = false
    if (!st.skipped) {
      st.skipped = true
      await $.store.set(SKIPPED, true).catch(() => undefined)
    }
  }
  await dialogGone($, st)
}

async function abortTurn($: EngineInterface, st: State) {
  if (st.working && st.turnId) await $.turn.abort({ turnId: st.turnId }).catch(() => undefined)
}

// A side question, answered from a fork of the session: tool-less, and outside the main conversation. With nothing
// to fork yet (no turn finished since the start, a resume or /clear), the session's model answers it alone.
async function fork($: EngineInterface, st: State, id: string, prompt: string) {
  let answer: Args
  try {
    let r = await $.model.fork({ prompt })
    if (!r.isAnswered && r.reason === 'nothing-to-fork') {
      r = await $.model.complete({ model: await $.session.model(), prompt, maxTokens: 16_000 })
    }
    answer = r.isAnswered ? { id, answered: true, text: r.text } : { id, answered: false, reason: r.reason }
  } catch (err) {
    answer = { id, answered: false, reason: err instanceof Error ? err.message : String(err) }
  }
  await host($, st, 'fork-answer', answer)
}

// connect says hello to Oriel and registers the tools it offers that Claude Code doesn't have.
async function connect($: EngineInterface, st: State) {
  const out = await host($, st, 'hello', { sessionId: st.sessionId, cwd: st.cwd })
  const tools = Array.isArray(out?.tools) ? (out.tools as Args[]) : []
  const have = await $.tool
    .list()
    .then(ts => new Set(ts.map(t => t.name)))
    .catch(() => new Set<string>())
  const registered = new Set([...st.tools].filter(([full]) => have.has(full)).map(([, name]) => name))
  for (const t of tools) {
    const name = String(t.name)
    if (registered.has(name)) continue
    const reg = await $.tool
      .register({
        name,
        description: String(t.description ?? ''),
        inputSchema: (t.inputSchema as Record<string, unknown>) ?? { type: 'object' },
      })
      .catch(() => undefined)
    st.tools.set(reg?.tool ?? `mcp__oriel__${name}`, name)
  }
}

// servers are the MCP servers named in tool names: mcp__<server>__<tool>.
function servers(names: string[]): Set<string> {
  const out = new Set<string>()
  for (const n of names) {
    const m = /^mcp__(.+?)__/.exec(n)
    if (m) out.add(m[1])
  }
  return out
}

// heal puts back what Claude Code dropped (a /login reconnects every MCP server): Oriel's own tools are registered
// again; an Oriel server that's gone only a restart brings back, so Oriel is told.
async function heal($: EngineInterface, st: State) {
  const names = await $.tool
    .list()
    .then(ts => ts.map(t => t.name))
    .catch(() => undefined)
  if (!names) return
  if ([...st.tools.keys()].some(n => !names.includes(n))) await connect($, st)
  const now = servers(names)
  const lost = [...st.servers].filter(s => !now.has(s))
  for (const s of now) if (s.startsWith('oriel-')) st.servers.add(s)
  if (lost.length > 0) await event($, st, { kind: 'tools-lost', servers: lost })
}

// poll long-polls Oriel for what to do next: a prompt to submit, a turn to abort, a question to fork.
async function poll($: EngineInterface, st: State) {
  for (;;) {
    await heal($, st)
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
      case 'tools':
        await connect($, st)
        break
    }
  }
}

// The plan's windows as Claude Code holds them now: none until a request or /usage has read them. error is why
// $.session.usage() failed.
async function reportUsage($: EngineInterface, st: State) {
  let rateLimits: unknown[] = []
  let error = ''
  try {
    rateLimits = (await $.session.usage()).rateLimits ?? []
  } catch (err) {
    error = err instanceof Error ? err.message : String(err)
  }
  await event($, st, { kind: 'usage', rateLimits, error })
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
    background: st.background,
  })
  st.background = []
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
    background: [],
    servers: new Set(),
    unattended: false,
    mode: '',
    pending: false,
    skipped: false,
    dialog: false,
  }

  on('session.start', async ($, e, next) => {
    const r = await next(e)
    if ((await $.env.get('ORIEL_SPAWNED')) !== '1' || st.active) return r
    st.active = true
    st.cwd = e.cwd
    st.sessionId = await $.session.id()
    st.bin = (await $.env.get('ORIEL_BIN')) || 'oriel'
    st.sock = (await $.env.get('ORIEL_HOOK_SOCK')) || ''
    st.unattended = (await $.env.get('ORIEL_UNATTENDED')) === '1'
    st.skipped = (await $.store.get(SKIPPED).catch(() => undefined)) === true
    await connect($, st)
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
    if (e.isAborted) st.pending = false // an interrupt can end a dialog too
    await settle($, st)
    void reportTurnEnd($, st, e as unknown as Args)
    return r
  })

  // Runs as the main loop stops, before its turn.complete: the mode is told first, so a restart at the turn's end
  // resumes in it.
  on('classic.Stop', async ($, e, next) => {
    if (st.active) st.background = e.background_tasks ?? []
    await noteMode($, st, e.permission_mode)
    return next(e)
  }).catch(passOn)

  on('classic.UserPromptSubmit', async ($, e, next) => {
    await noteMode($, st, e.permission_mode)
    return next(e)
  }).catch(passOn)

  // A turn the API ended once Claude Code's own retries ran out.
  on('classic.StopFailure', async ($, e, next) => {
    if (st.active) void event($, st, { kind: 'api-error', error: e.error, text: e.error_details ?? e.last_assistant_message ?? '' })
    return next(e)
  }).catch(passOn)

  // The guard rails are Oriel's: it allows, denies, asks, or leaves the call to the mode and its dialog. Its ask
  // fires PermissionRequest in every mode, which Oriel answers below; a deny or a rule of the user's own settings
  // (core's verdict) still stands over it.
  on('tool.check', async ($, e, next) => {
    if (!st.active) return next(e)
    await settle($, st)
    const v = await check($, st, e, next)
    // A person answers it: Oriel's PermissionRequest hook below, or, where the sec-default plugin continues past that
    // hook, only the engine's dialog in the terminal, which Oriel is told of here. Auto mode's classifier answers
    // the mode's own asks; an organization's ceiling caps an allow at an ask.
    const asks = v.decision === 'ask' && (v.plugin !== undefined || st.mode !== 'auto')
    const real = e.tool_use_id !== undefined && e.approver !== 'host' // a call, not a query, and no host's callback answers it
    if (real && e.tool !== 'AskUserQuestion' && (asks || (v.decision === 'allow' && e.ceiling === 'ask'))) {
      st.pending = true
      if (st.skipped) {
        st.dialog = true
        await event($, st, { kind: 'dialog', tool: e.tool, input: e.input })
      }
    }
    return v
  }).catch(passOn)

  // Fires only when the engine is about to open its dialog. Oriel answers it instead; with no answer from Oriel
  // the dialog opens as usual.
  let permSeq = 0
  on('classic.PermissionRequest', async ($, e, next) => {
    await noteMode($, st, e.permission_mode)
    const r = await next(e)
    if (!st.active || e.tool_name === 'AskUserQuestion') return r
    st.pending = false
    if (st.skipped) {
      st.skipped = false // reached after all: whatever skipped it no longer does
      await $.store.set(SKIPPED, false).catch(() => undefined)
    }
    if (r.decision) {
      await dialogGone($, st)
      return r
    }
    st.dialog = false // the permission Oriel is asked takes the dialog's place
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
    if (st.unattended) return { ...r, decision: { behavior: 'deny', message: NOBODY } }
    return r
  }).catch(passOn)


  on('tool.call', { tool: 'AskUserQuestion' }, async ($, e, next) => {
    if (!st.active) return next(e)
    const questions = (e as unknown as Args).questions
    const out = await host($, st, 'question', { id: e.tool_use_id, input: { questions } }, 600_000)
    if (out?.deny) return { deny: String(out.deny) }
    if (!out?.answers && st.unattended) return { deny: NOBODY }
    if (!out?.answers) return next(e)
    return { result: { questions, answers: out.answers } } as never
  }).catch(passOn)

  // Oriel's own tools run on the host.
  on('tool.call', async ($, e, next) => {
    await settle($, st)
    const name = st.tools.get(e.tool)
    if (!st.active || name === undefined) return next(e)
    const { tool: _tool, tool_use_id, agentId: _agent, requestMeta: _meta, ...input } = e as unknown as Args
    const out = await host($, st, 'tool', { name, id: tool_use_id, input }, 600_000)
    if (!out) return { isError: true, result: `Oriel did not answer the ${name} call.` }
    return out.isError ? { isError: true, result: String(out.result ?? '') } : { result: String(out.result ?? '') }
  }).catch(passOn)
}
