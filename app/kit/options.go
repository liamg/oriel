package kit

// BlankIfDefault is an option's value as the code below it takes it: "" for the named value that stands for its own choice
// ("auto", "default"), so the setting shows what it does rather than an empty field.
func BlankIfDefault(v, named string) string {
	if v == named {
		return ""
	}
	return v
}
