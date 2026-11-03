package kit

import "time"

// UnfocusedPoll is how often network polls run while the window hasn't focus: a watch.Need's Slow.
const UnfocusedPoll = 5 * time.Minute
