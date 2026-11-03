package explain

// What the package's outside tests read of its plugins.

// MarkOpen, MarkExplained and MarkChanged are the styles of a unit's ? while its card is open, once it's explained,
// and once it's changed since; MarkBranch and MarkUnchanged, of one the branch changed and one it didn't.
func MarkOpen() string      { return tExplainMarkOn.Name() }
func MarkExplained() string { return tUnitsDone.Name() }
func MarkChanged() string   { return tUnitsChanged.Name() }
func MarkBranch() string    { return tExplainMark.Name() }
func MarkUnchanged() string { return tExplainMarkQuiet.Name() }

// ChangedRows are the labels of the Changed units panel's rows, as it last worked them out.
func (x *Plugin) ChangedRows() []string {
	files, _ := x.changedFiles()
	var rows []string
	for _, f := range files {
		for _, u := range f.units {
			rows = append(rows, u.label)
		}
	}
	return rows
}
