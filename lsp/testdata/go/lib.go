package greet

import "fmt"

// greeting says hello to name.
func greeting(name string) string {
	return fmt.Sprintf("héllo, %s", name)
}

// shout greets name loudly.
func shout(name string) string {
	return greeting(name) + "!"
}
