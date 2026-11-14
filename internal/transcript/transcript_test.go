package transcript

import (
	"bytes"
	"testing"
)

type header struct {
	Server string `json:"server"`
}

// A transcript written is the one read back.
func TestRoundTrip(t *testing.T) {
	tr := &Transcript[header]{Header: header{Server: "gopls"}, Entries: []Entry{{Dir: Send, Msg: []byte(`{"id":1}`)}, {Dir: Recv, Msg: []byte(`{"id":1,"result":null}`)}}}
	var b bytes.Buffer
	if err := tr.Write(&b); err != nil {
		t.Fatal(err)
	}
	got, err := Parse[header](b.Bytes())
	if err != nil || got.Header.Server != "gopls" || len(got.Entries) != 2 || string(got.Entries[1].Msg) != `{"id":1,"result":null}` {
		t.Errorf("read back %+v, %v", got, err)
	}
	if _, err := Parse[header]([]byte("{}\n{\"dir\":\"sideways\",\"msg\":{}}\n")); err == nil {
		t.Error("a line with no direction reads")
	}
}

// The machine's paths become variables, the longest first, so a root inside the home directory stays ${root}; and
// back again.
func TestVars(t *testing.T) {
	v := Vars{RootVar: "/home/ada/src/app", "${home}": "/home/ada"}
	msg := []byte(`{"uri":"file:///home/ada/src/app/main.go","cache":"/home/ada/.cache"}`)
	abs := v.Abstract(msg)
	if want := `{"uri":"file://${root}/main.go","cache":"${home}/.cache"}`; string(abs) != want {
		t.Errorf("abstract = %s, want %s", abs, want)
	}
	if back := v.Concrete(abs); !bytes.Equal(back, msg) {
		t.Errorf("concrete = %s", back)
	}
}
