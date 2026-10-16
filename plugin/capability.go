package plugin

// Capability is something a plugin may do, declared in its manifest's [permissions] (security.md).
type Capability string

const (
	CapUI             Capability = "ui"
	CapTheme          Capability = "theme"
	CapEditorRead     Capability = "editor.read"
	CapEditorWrite    Capability = "editor.write"
	CapInputObserve   Capability = "input.observe"
	CapFSRead         Capability = "fs.read"
	CapFSWrite        Capability = "fs.write"
	CapNet            Capability = "net"
	CapSecrets        Capability = "secrets"
	CapExec           Capability = "exec"
	CapAgentRead      Capability = "agent.read"
	CapAgentPrompt    Capability = "agent.prompt"
	CapAgentTools     Capability = "agent.tools"
	CapGate           Capability = "gate"
	CapSessionControl Capability = "session.control"
	CapGit            Capability = "git"
	CapClipboard      Capability = "clipboard"
	CapNotify         Capability = "notify"
)

// Risk is how much harm a capability can do, as the install screen shows it.
type Risk int

const (
	RiskLow Risk = iota
	RiskMedium
	RiskHigh
	RiskCritical
)

// CapInfo is what Oriel says about a capability: how risky it is, whether install lists it and warns about it,
// and the scope its grant takes ("" for none).
type CapInfo struct {
	Cap    Capability
	Risk   Risk
	Shown  bool
	Warned bool
	Scope  string // "hosts", "paths", "commands", "tools", "names"
}

// Capabilities is every capability, in security.md's order.
var Capabilities = []CapInfo{
	{CapUI, RiskLow, false, false, ""},
	{CapTheme, RiskLow, false, false, ""},
	{CapEditorRead, RiskMedium, true, false, ""},
	{CapEditorWrite, RiskMedium, true, false, ""},
	{CapInputObserve, RiskHigh, true, true, ""},
	{CapFSRead, RiskMedium, true, false, "paths"},
	{CapFSWrite, RiskHigh, true, false, "paths"},
	{CapNet, RiskMedium, true, false, "hosts"},
	{CapSecrets, RiskHigh, true, false, "names"},
	{CapExec, RiskHigh, true, true, "commands"},
	{CapAgentRead, RiskHigh, true, false, ""},
	{CapAgentPrompt, RiskHigh, true, true, ""},
	{CapAgentTools, RiskHigh, true, true, "tools"},
	{CapGate, RiskCritical, true, true, ""},
	{CapSessionControl, RiskMedium, true, false, ""},
	{CapGit, RiskHigh, true, false, ""},
	{CapClipboard, RiskMedium, true, false, ""},
	{CapNotify, RiskLow, false, false, ""},
}

// Info is what Oriel says about c; ok is false for a capability it doesn't know.
func (c Capability) Info() (CapInfo, bool) {
	for _, i := range Capabilities {
		if i.Cap == c {
			return i, true
		}
	}
	return CapInfo{}, false
}

// Always is a capability every plugin has without declaring it: ui, theme and notify.
func (c Capability) Always() bool {
	i, ok := c.Info()
	return ok && !i.Shown
}
