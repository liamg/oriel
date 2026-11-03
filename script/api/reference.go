package api

// RefFunc is one function in the API's reference: how it's called, what it does, and the capability a plugin needs
// to call it ("target" when it checks its own, against what it's called on).
type RefFunc struct {
	Call string
	Doc  string
	Cap  string
}
