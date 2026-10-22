package gio

import (
	"bufio"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
)

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// fixtureTree is a start page and an editor's controls, with every role the bridges map.
func fixtureTree() *render.AccessNode {
	box := func(x, y, w, h float32) render.Rect { return render.XYWH(x, y, w, h) }
	return &render.AccessNode{ID: 0, Role: render.RoleWindow, Name: "Oriel", Bounds: box(0, 0, 800, 600), Children: []*render.AccessNode{
		{ID: 1, Role: render.RoleRegion, Name: "Main", Bounds: box(0, 40, 800, 520), Children: []*render.AccessNode{
			{ID: 2, Role: render.RoleToolbar, Name: "Actions", Bounds: box(0, 40, 800, 30), Children: []*render.AccessNode{
				{ID: 3, Role: render.RoleButton, Name: "Open Directory…", Bounds: box(10, 45, 120, 20), Actions: []render.Action{render.ActionClick, render.ActionFocus}},
				{ID: 4, Role: render.RoleButton, Name: "Run", Disabled: true, Bounds: box(140, 45, 60, 20), Actions: []render.Action{render.ActionClick}},
			}},
			{ID: 5, Role: render.RoleTextBox, Name: "Search", Value: "héllo 😀 wörld", Selection: render.TextRange{Start: 6, End: 7}, Focused: true,
				Bounds: box(10, 80, 300, 24), Actions: []render.Action{render.ActionClick, render.ActionFocus}},
			{ID: 6, Role: render.RoleList, Name: "Recent", Bounds: box(10, 110, 300, 100), Children: []*render.AccessNode{
				{ID: 7, Role: render.RoleListItem, Name: "oriel", Selected: true, Bounds: box(10, 110, 300, 20), Actions: []render.Action{render.ActionClick}},
				{ID: 8, Role: render.RoleListItem, Name: "crit", Bounds: box(10, 130, 300, 20), Actions: []render.Action{render.ActionClick}},
			}},
			{ID: 9, Role: render.RoleTabList, Bounds: box(320, 80, 400, 24), Children: []*render.AccessNode{
				{ID: 10, Role: render.RoleTab, Name: "Files", Selected: true, Bounds: box(320, 80, 80, 24), Actions: []render.Action{render.ActionClick}},
			}},
			{ID: 11, Role: render.RoleCheckBox, Name: "Wrap", Checked: yes(), Bounds: box(320, 110, 80, 20), Actions: []render.Action{render.ActionClick, render.ActionFocus}},
			{ID: 12, Role: render.RoleLink, Name: "Docs", Bounds: box(320, 140, 80, 20), Actions: []render.Action{render.ActionClick}},
			{ID: 13, Role: render.RoleMenu, Name: "File", Bounds: box(400, 110, 100, 60), Children: []*render.AccessNode{
				{ID: 14, Role: render.RoleMenuItem, Name: "Save", Bounds: box(400, 110, 100, 20), Actions: []render.Action{render.ActionClick}},
			}},
			{ID: 15, Role: render.RoleSlider, Name: "Zoom", Value: "100", Bounds: box(510, 110, 100, 20),
				Actions: []render.Action{render.ActionIncrement, render.ActionDecrement}},
			{ID: 16, Role: render.RoleImage, Name: "Logo", Bounds: box(620, 110, 40, 40)},
			{ID: 17, Role: render.RoleTree, Name: "Files", Bounds: box(10, 220, 300, 100), Children: []*render.AccessNode{
				{ID: 18, Role: render.RoleTreeItem, Name: "src", Expanded: yes(), Bounds: box(10, 220, 300, 20), Actions: []render.Action{render.ActionClick}},
				{ID: 19, Role: render.RoleTreeItem, Name: "docs", Expanded: no(), Bounds: box(10, 240, 300, 20), Actions: []render.Action{render.ActionClick}},
			}},
			{ID: 20, Role: render.RoleText, Name: "Welcome back", Bounds: box(10, 330, 300, 20)},
			{ID: 21, Role: render.RoleGroup, Bounds: box(10, 360, 300, 20)},
		}},
		{ID: 22, Role: render.RoleStatus, Name: "Ready", Bounds: box(0, 580, 800, 20)},
		{ID: 23, Role: render.RoleDialog, Name: "Settings", Modal: true, Bounds: box(200, 150, 400, 300)},
	}}
}

func find(n *render.AccessNode, id uint64) *render.AccessNode {
	if n.ID == id {
		return n
	}
	for _, k := range n.Children {
		if f := find(k, id); f != nil {
			return f
		}
	}
	return nil
}

func TestAccessTreeMirrorsUpdates(t *testing.T) {
	var tr accessTree
	first := fixtureTree()
	c := tr.apply(render.AccessDiff(nil, first))
	if len(c.Added) != 24 || !c.Moved || c.Focus != 5 {
		t.Fatalf("first update: %d added, focus %d moved %v", len(c.Added), c.Focus, c.Moved)
	}
	if e, ok := tr.get(7); !ok || e.parent != 6 || e.node.Name != "oriel" || e.node.Children != nil {
		t.Errorf("node 7: %+v %v", e, ok)
	}
	if e, _ := tr.get(1); !slices.Equal(e.kids, []uint64{2, 5, 6, 9, 11, 12, 13, 15, 16, 17, 20, 21}) {
		t.Errorf("node 1's kids: %v", e.kids)
	}
	if got := tr.index(8); got != 1 {
		t.Errorf("index of 8: %d", got)
	}

	// A frame that changes nothing changes nothing.
	if c := tr.apply(render.AccessDiff(first, fixtureTree())); !c.empty() {
		t.Errorf("same tree: %+v", c)
	}

	next := fixtureTree()
	find(next, 3).Name = "Open…"
	find(next, 5).Value = "héllo"
	find(next, 5).Focused = false
	find(next, 11).Focused = true
	find(next, 19).Expanded = yes()
	l := find(next, 6)
	l.Children = []*render.AccessNode{l.Children[1], {ID: 30, Role: render.RoleListItem, Name: "new"}}
	c = tr.apply(render.AccessDiff(first, next))
	if !slices.Equal(c.Added, []uint64{30}) || !slices.Equal(c.Removed, []uint64{7}) {
		t.Errorf("added %v removed %v", c.Added, c.Removed)
	}
	if !c.Moved || c.Blurred != 5 || c.Focus != 11 {
		t.Errorf("focus: %+v", c)
	}
	what := map[uint64]changeKind{}
	for _, ch := range c.Changed {
		what[ch.ID] = ch.What
	}
	want := map[uint64]changeKind{3: changeName, 5: changeValue | changeStates, 6: changeChildren, 11: changeStates, 19: changeStates}
	for id, k := range want {
		if what[id] != k {
			t.Errorf("node %d changed %b, want %b", id, what[id], k)
		}
	}
	if len(what) != len(want) {
		t.Errorf("changed %v, want %v", what, want)
	}
	if _, ok := tr.get(7); ok {
		t.Error("node 7 is still there")
	}
	if e, _ := tr.get(30); e.parent != 6 {
		t.Errorf("node 30's parent: %d", e.parent)
	}
}

func TestAccessHitIsDeepestTopmost(t *testing.T) {
	var tr accessTree
	tr.apply(render.AccessDiff(nil, fixtureTree()))
	for _, c := range []struct {
		at   render.Point
		want uint64
	}{
		{render.Point{X: 20, Y: 50}, 3},
		{render.Point{X: 20, Y: 115}, 7},
		{render.Point{X: 5, Y: 590}, 22},
		{render.Point{X: 700, Y: 10}, 0},
		{render.Point{X: 250, Y: 200}, 23}, // the dialog is over the list
	} {
		if got := tr.hit(c.at); got != c.want {
			t.Errorf("hit %v: %d, want %d", c.at, got, c.want)
		}
	}
}

func TestAccessActDoesNotBlock(t *testing.T) {
	a := newAccess(func() accessGeom { return accessGeom{} })
	a.tree.apply(render.AccessDiff(nil, fixtureTree()))
	if a.act(render.AccessAction{ID: 4, Action: render.ActionClick}) {
		t.Error("a disabled button took a click")
	}
	if a.act(render.AccessAction{ID: 99, Action: render.ActionClick}) {
		t.Error("a node that isn't there took a click")
	}
	done := make(chan int)
	go func() {
		n := 0
		for range 100 {
			if a.act(render.AccessAction{ID: 3, Action: render.ActionClick}) {
				n++
			}
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n != cap(a.actions) {
			t.Errorf("queued %d, want %d", n, cap(a.actions))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("act blocked on a full queue")
	}
}

func TestUTF16Ranges(t *testing.T) {
	s := "a😀b" // 😀 is two UTF-16 units
	for _, c := range []struct {
		r        render.TextRange
		loc, len int
	}{
		{render.TextRange{Start: 0, End: 0}, 0, 0},
		{render.TextRange{Start: 1, End: 2}, 1, 2},
		{render.TextRange{Start: 2, End: 3}, 3, 1},
		{render.TextRange{Start: 3, End: 3}, 4, 0},
		{render.TextRange{Start: 0, End: 3}, 0, 4},
	} {
		loc, n := toUTF16(s, c.r)
		if loc != c.loc || n != c.len {
			t.Errorf("toUTF16 %v: %d,%d want %d,%d", c.r, loc, n, c.loc, c.len)
		}
		if back := fromUTF16(s, loc, n); back != c.r {
			t.Errorf("fromUTF16 %d,%d: %v want %v", loc, n, back, c.r)
		}
	}
}

func TestTextHelpers(t *testing.T) {
	if at, del, ins := textEdit("hello world", "hello brave world"); at != 6 || del != "" || ins != "brave " {
		t.Errorf("insert: %d %q %q", at, del, ins)
	}
	if at, del, ins := textEdit("héllo", "hé"); at != 2 || del != "llo" || ins != "" {
		t.Errorf("delete: %d %q %q", at, del, ins)
	}
	if s, a, b := textAround("one two\nthree", 5, unitWord); s != "two\n" || a != 4 || b != 8 {
		t.Errorf("word: %q %d %d", s, a, b)
	}
	if s, a, b := textAround("one two\nthree", 9, unitLine); s != "three" || a != 8 || b != 13 {
		t.Errorf("line: %q %d %d", s, a, b)
	}
	if s, a, b := textAround("héllo", 1, unitChar); s != "é" || a != 1 || b != 2 {
		t.Errorf("char: %q %d %d", s, a, b)
	}
	if got := spliced("héllo", 1, 2, "e"); got != "hello" {
		t.Errorf("spliced: %q", got)
	}
}

func TestElementMapKeepsOnePerNode(t *testing.T) {
	made, freed := map[uint64]int{}, map[uint64]uintptr{}
	next := uintptr(100)
	m := elementMap{
		make: func(id uint64) uintptr { made[id]++; next++; return next },
		free: func(id uint64, h uintptr) { freed[id] = h },
	}
	a, b := m.element(3), m.element(3)
	if a != b || made[3] != 1 {
		t.Errorf("element 3 made %d times: %d, %d", made[3], a, b)
	}
	if _, ok := m.existing(4); ok {
		t.Error("element 4 exists before anyone asked")
	}
	c := m.element(4)
	m.drop([]uint64{3, 99})
	if freed[3] != a || len(freed) != 1 {
		t.Errorf("freed %v", freed)
	}
	if h := m.element(3); h == a || made[3] != 2 {
		t.Error("a node that came back reused a freed element")
	}
	m.clear()
	if freed[4] != c || m.len() != 0 {
		t.Errorf("clear freed %v, %d left", freed, m.len())
	}
}

// atspiEnums is testdata/atspi-enums.txt: libatspi's own numbers and role names.
func atspiEnums(t *testing.T) (values map[string]uint32, names map[uint32]string) {
	f, err := os.Open("testdata/atspi-enums.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	values, names = map[string]uint32{}, map[uint32]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "name "); ok {
			n, name, _ := strings.Cut(rest, " ")
			v, _ := strconv.Atoi(n)
			names[uint32(v)] = name
			continue
		}
		k, v, _ := strings.Cut(line, " ")
		n, _ := strconv.Atoi(v)
		values[k] = uint32(n)
	}
	return values, names
}

func TestATSPIMapping(t *testing.T) {
	values, names := atspiEnums(t)
	want := map[uint64]struct {
		role   string
		states []string
	}{
		0:  {"FRAME", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "ACTIVE"}},
		1:  {"LANDMARK", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		2:  {"TOOL_BAR", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		3:  {"PUSH_BUTTON", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "FOCUSABLE"}},
		4:  {"PUSH_BUTTON", []string{"VISIBLE", "SHOWING"}},
		5:  {"ENTRY", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "FOCUSABLE", "FOCUSED", "EDITABLE", "SELECTABLE_TEXT"}},
		6:  {"LIST", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		7:  {"LIST_ITEM", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "SELECTABLE", "SELECTED"}},
		9:  {"PAGE_TAB_LIST", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		10: {"PAGE_TAB", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "SELECTABLE", "SELECTED"}},
		11: {"CHECK_BOX", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "FOCUSABLE", "CHECKABLE", "CHECKED"}},
		12: {"LINK", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		13: {"MENU", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		14: {"MENU_ITEM", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		15: {"SLIDER", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		16: {"IMAGE", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		17: {"TREE", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		18: {"TREE_ITEM", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "SELECTABLE", "EXPANDABLE", "EXPANDED"}},
		19: {"TREE_ITEM", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "SELECTABLE", "EXPANDABLE", "COLLAPSED"}},
		20: {"LABEL", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		21: {"PANEL", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		22: {"STATUS_BAR", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING"}},
		23: {"DIALOG", []string{"ENABLED", "SENSITIVE", "VISIBLE", "SHOWING", "MODAL"}},
	}
	tree := fixtureTree()
	for id, w := range want {
		n := find(tree, id)
		role := atspiRole(n.Role)
		if v, ok := values["ATSPI_ROLE_"+w.role]; !ok || role != v {
			t.Errorf("node %d (%s): role %d, want ATSPI_ROLE_%s (%d)", id, n.Role, role, w.role, v)
		}
		if atspiRoleNames[role] != names[role] {
			t.Errorf("role %d's name %q, libatspi's %q", role, atspiRoleNames[role], names[role])
		}
		var got, exp []uint32
		for _, s := range atspiStates(n, true) {
			got = append(got, uint32(s))
			if values["ATSPI_STATE_"+strings.ToUpper(strings.ReplaceAll(atspiStateNames[s], "-", "_"))] != uint32(s) {
				t.Errorf("state %d is named %q, which libatspi numbers otherwise", s, atspiStateNames[s])
			}
		}
		for _, s := range w.states {
			v, ok := values["ATSPI_STATE_"+s]
			if !ok {
				t.Fatalf("no ATSPI_STATE_%s", s)
			}
			exp = append(exp, v)
		}
		slices.Sort(exp)
		if !slices.Equal(got, exp) {
			t.Errorf("node %d (%s): states %v, want %v", id, n.Role, got, exp)
		}
	}
	if got := atspiStateSet([]atspiState{stateEnabled, stateCheckable}); !slices.Equal(got, []uint32{1 << 8, 1 << (41 - 32)}) {
		t.Errorf("state set: %b", got)
	}
	if got := atspiInterfaces(find(tree, 5)); !slices.Contains(got, "org.a11y.atspi.EditableText") || !slices.Contains(got, "org.a11y.atspi.Action") {
		t.Errorf("textbox interfaces: %v", got)
	}
	if got := atspiInterfaces(find(tree, 15)); !slices.Contains(got, "org.a11y.atspi.Value") {
		t.Errorf("slider interfaces: %v", got)
	}
	if got := atspiInterfaces(find(tree, 16)); slices.Contains(got, "org.a11y.atspi.Action") {
		t.Errorf("image interfaces: %v", got)
	}
}

func TestATSPIPaths(t *testing.T) {
	p := atspiPath(2, 1<<40)
	if w, id, ok := atspiParsePath(p); !ok || w != 2 || id != 1<<40 {
		t.Errorf("%s: %d %d %v", p, w, id, ok)
	}
	for _, bad := range []string{atspiPrefix + "/root", atspiPrefix + "/x_1", "/elsewhere/1_2"} {
		if _, _, ok := atspiParsePath(bad); ok {
			t.Errorf("%s parsed", bad)
		}
	}
}

func TestATSPIEvents(t *testing.T) {
	var tr accessTree
	first := fixtureTree()
	tr.apply(render.AccessDiff(nil, first))
	next := fixtureTree()
	find(next, 3).Name = "Open…"
	s := find(next, 5)
	s.Value, s.Selection, s.Focused = "héllo there 😀 wörld", render.TextRange{Start: 11, End: 11}, false
	find(next, 11).Focused = true
	find(next, 11).Checked = no()
	find(next, 19).Expanded = yes()
	find(next, 4).Disabled = false
	l := find(next, 6)
	l.Children = []*render.AccessNode{l.Children[1], {ID: 30, Role: render.RoleListItem, Name: "new"}}
	got := atspiEvents(tr.apply(render.AccessDiff(first, next)), &tr)
	var lines []string
	for _, e := range got {
		lines = append(lines, strings.TrimSpace(strings.Join([]string{strconv.FormatUint(e.ID, 10), e.Member, e.Kind,
			strconv.Itoa(int(e.Detail1)), strconv.Itoa(int(e.Detail2)), describe(e.Data)}, " ")))
	}
	want := []string{
		"3 Object.PropertyChange accessible-name 0 0 Open…",
		"4 Object.StateChanged enabled 1 0",
		"4 Object.StateChanged sensitive 1 0",
		`5 Object.TextChanged insert 6 6 there`,
		"5 Object.TextCaretMoved  11 0",
		"5 Object.TextSelectionChanged  0 0",
		"6 Object.ChildrenChanged remove 0 0 node 7",
		"6 Object.ChildrenChanged add 1 0 node 30",
		"11 Object.StateChanged checked 0 0",
		"19 Object.StateChanged expanded 1 0",
		"5 Object.StateChanged focused 0 0",
		"11 Object.StateChanged focused 1 0",
		"11 Focus.Focus  0 0",
	}
	slices.Sort(lines[:len(lines)-3])
	slices.Sort(want[:len(want)-3])
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func describe(d any) string {
	switch d := d.(type) {
	case nil:
		return ""
	case atspiNodeRef:
		return "node " + strconv.FormatUint(uint64(d), 10)
	case string:
		return strings.TrimSpace(d)
	}
	return "?"
}

func TestATSPITextAt(t *testing.T) {
	s := "one two\nthree"
	for _, c := range []struct {
		off      int
		kind     uint32
		granular bool
		dir      int
		want     string
		a, b     int
	}{
		{4, 0, true, 0, "t", 4, 5},
		{4, 1, true, 0, "two\n", 4, 8},
		{4, 3, true, 0, "one two\n", 0, 8},
		{4, 1, false, -1, "one ", 0, 4},
		{4, 1, false, 1, "three", 8, 13},
		{9, 5, false, -1, "one two\n", 0, 8},
		{9, 5, false, 1, "", 13, 13},
	} {
		got, a, b := atspiTextAt(s, c.off, c.kind, c.granular, c.dir)
		if got != c.want || a != c.a || b != c.b {
			t.Errorf("%+v: %q %d %d", c, got, a, b)
		}
	}
}

func TestAppKitMapping(t *testing.T) {
	// Roles map to NSAccessibilityRoles (button, text field, list, row, toolbar, group, static text,
	// link, tab, check box, menu). A tab is AppKit's tab button: a radio button with the tab button subrole.
	want := map[uint64]struct {
		role appkitRole
		sub  appkitSubrole
	}{
		0: {axGroup, axNoSubrole}, 1: {axGroup, axNoSubrole}, 2: {axToolbar, axNoSubrole}, 3: {axButton, axNoSubrole},
		5: {axTextField, axNoSubrole}, 6: {axList, axNoSubrole}, 7: {axRow, axNoSubrole}, 9: {axTabGroup, axNoSubrole},
		10: {axRadioButton, axTabButton}, 11: {axCheckBox, axNoSubrole}, 12: {axLink, axNoSubrole}, 13: {axMenu, axNoSubrole},
		14: {axMenuItem, axNoSubrole}, 15: {axSlider, axNoSubrole}, 16: {axImage, axNoSubrole}, 17: {axOutline, axNoSubrole},
		18: {axRow, axNoSubrole}, 20: {axStaticText, axNoSubrole}, 21: {axGroup, axNoSubrole}, 22: {axGroup, axNoSubrole},
		23: {axGroup, axDialog},
	}
	tree := fixtureTree()
	for id, w := range want {
		n := find(tree, id)
		if r, s := appkitRoleOf(n.Role); r != w.role || s != w.sub {
			t.Errorf("node %d (%s): %d/%d, want %d/%d", id, n.Role, r, s, w.role, w.sub)
		}
	}
	for _, c := range []struct {
		id    uint64
		kind  appkitValueKind
		s     string
		num   int
		label string
	}{
		{5, axStringValue, "héllo 😀 wörld", 0, "Search"},
		{20, axStringValue, "Welcome back", 0, ""},
		{11, axNumberValue, "", 1, "Wrap"},
		{10, axNumberValue, "", 1, "Files"},
		{15, axStringValue, "100", 0, "Zoom"},
		{3, axNoValue, "", 0, "Open Directory…"},
	} {
		n := find(tree, c.id)
		k, s, num := appkitValue(n)
		if k != c.kind || s != c.s || num != c.num || appkitLabel(n) != c.label {
			t.Errorf("node %d: value %d %q %d label %q", c.id, k, s, num, appkitLabel(n))
		}
	}
}

func TestAppKitNotes(t *testing.T) {
	var tr accessTree
	first := fixtureTree()
	tr.apply(render.AccessDiff(nil, first))
	next := fixtureTree()
	find(next, 3).Name = "Open…"
	s := find(next, 5)
	s.Value, s.Selection, s.Focused = "héllo", render.TextRange{Start: 5, End: 5}, false
	find(next, 11).Focused = true
	find(next, 19).Expanded = yes()
	find(next, 22).Name = "Saved"
	got := appkitNotes(tr.apply(render.AccessDiff(first, next)), &tr)
	want := []appkitNote{
		{Kind: noteTitle, ID: 3}, {Kind: noteValue, ID: 5}, {Kind: noteSelectedText, ID: 5}, {Kind: noteExpanded, ID: 19},
		{Kind: noteTitle, ID: 22}, {Kind: noteAnnounce, ID: 22, Text: "Saved"}, {Kind: noteFocus, ID: 11},
	}
	if !slices.Equal(got, want) {
		t.Errorf("notes:\n%v\nwant\n%v", got, want)
	}
	// A frame that changes nothing posts nothing.
	if got := appkitNotes(tr.apply(render.AccessDiff(next, next)), &tr); len(got) != 0 {
		t.Errorf("same tree posted %v", got)
	}
}

// access_darwin.h's enums are the Go lists' C side, in the same order.
func TestAppKitHeaderMatchesGo(t *testing.T) {
	h, err := os.ReadFile("access_darwin.h")
	if err != nil {
		t.Fatal(err)
	}
	src := string(h)
	order := func(first string) []string {
		i := strings.Index(src, first)
		if i < 0 {
			t.Fatalf("no %s in the header", first)
		}
		body := src[i : strings.Index(src[i:], "}")+i]
		var out []string
		for _, f := range strings.Split(body, ",") {
			if f = strings.TrimSpace(f); f != "" {
				out = append(out, f)
			}
		}
		return out
	}
	check := func(got []string, want map[string]int) {
		for i, name := range got {
			if v, ok := want[name]; !ok || v != i {
				t.Errorf("%s is %d in the header, %d (%v) in Go", name, i, v, ok)
			}
		}
		if len(got) != len(want) {
			t.Errorf("header has %d, Go %d", len(got), len(want))
		}
	}
	check(order("orielAXGroup"), map[string]int{
		"orielAXGroup": int(axGroup), "orielAXToolbar": int(axToolbar), "orielAXStaticText": int(axStaticText),
		"orielAXButton": int(axButton), "orielAXTextField": int(axTextField), "orielAXList": int(axList),
		"orielAXRow": int(axRow), "orielAXOutline": int(axOutline), "orielAXTabGroup": int(axTabGroup),
		"orielAXRadioButton": int(axRadioButton), "orielAXMenu": int(axMenu), "orielAXMenuItem": int(axMenuItem),
		"orielAXSlider": int(axSlider), "orielAXImage": int(axImage), "orielAXLink": int(axLink),
		"orielAXCheckBox": int(axCheckBox), "orielAXRoleCount": int(axRoleCount),
	})
	check(order("orielAXNoSubrole"), map[string]int{
		"orielAXNoSubrole": int(axNoSubrole), "orielAXTabButton": int(axTabButton), "orielAXDialog": int(axDialog),
		"orielAXSubroleCount": int(axSubroleCount),
	})
	check(order("orielAXNoteFocus"), map[string]int{
		"orielAXNoteFocus": int(noteFocus), "orielAXNoteTitle": int(noteTitle), "orielAXNoteValue": int(noteValue),
		"orielAXNoteSelectedText": int(noteSelectedText), "orielAXNoteLayout": int(noteLayout),
		"orielAXNoteAnnounce": int(noteAnnounce), "orielAXNoteExpanded": int(noteExpanded),
		"orielAXNoteCollapsed": int(noteCollapsed), "orielAXNoteCount": int(noteCount),
	})
}
