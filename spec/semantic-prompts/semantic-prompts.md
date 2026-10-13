* Name: semantic-prompts
* Start Date: 2019-05-11

# Summary

Escape codes and conventions for semantic marking of
commands (as in shells and other REPLs) and their components
(prompts, user inputs, and output).

[This article shows enabled functionality with screenhots](http://per.bothner.com/blog/2019/shell-integration-proposal/),
based on how the proposal is implemented in [DomTerm](https://domterm.org).

# Motivation

The user interaction in shells and other [REPL applications](https://en.wikipedia.org/wiki/Read-eval-print-loop) is a sequence of commands,
where each _command_ is a sequence of _user input_ (one or more lines typed by the user) and command output (from the application).
The input lines will usually contain _prompt_ sections emitted
by the application, usually at the beginning of the first line.

This protocol defines a way for an application to specify to a terminal
the start and end of commands and their parts: input, prompt, and output.
This can enable useful features like visual separation/highlighting
of commands; folding (a button to hide long command output);
searching or copying of command input or output; or indication of return status.

This specification is an extension of the protocol
first implemented by [FinalTerm](https://github.com/p-e-w/finalterm) and
then later by [iterm2](https://iterm2.com/documentation-shell-integration.html).
Other similar prior art is known for
[Extraterm](http://extraterm.org/), [DomTerm](http://domterm.org/Wire-byte-protocol.html),
[XMLterm](www.xml.com/pub/2000/06/07/xmlterm/index.html),
and [GraphTerm](https://github.com/mitotic/graphterm).

# Detail

A command sequence may be nested withing the output section of
another command, for example when the user types a shell command
to invoke some other REPL application.  To help track this
nesting we make use of an optional _application identifier_ (`aid`)
which can be an arbirary string specified by the application.
It is suggested that the `aid` be (or contain) the process id of the
application; if that is unavailable, the name of the application.

## Commands

`OSC "133;L\007"`

> Do a _fresh-line_: If the cursor is the initial column
> (left, assuming left-to-right writing), do nothing.
> Otherwise, do the equivalent of `"\r\n"`.

**Issue**: Possibly add an option for a character (with styling)
to indicate that a newline is missing.
For example fish uses `⏎`, while zsh uses `%` (in inverse video).

`OSC "133;A"` _options_ `"\007"`

> First do a _fresh-line_.
> Then start a new command, and enter prompt mode: Subsequent text
> (until a `OSC "133;B"` or `OSC "133;I"` command) is a prompt string
> (as if followed by `OSC 133;P;k=i\007`).

> Applicable options: `aid`, `cl`.

`OSC "133;N"` _options_ `"\007"`

> Same as `OSC "133;A"` but may first implicitly terminate a previous command:
> If the _options_ specify an `aid` and there is an active (open)
> command with matching `aid`, finish the innermost such command
> (as well as any other commands nested more deeply).
> If no `aid` is specified, treat as an `aid` whose value is the empty string.

> Applicable options: `aid`, `cl`.

`OSC "133;P"` _options_ `"\007"`

> Explicit start of prompt. Optional after an `A` or `N` command.
> The `k` (kind) option specifies the type of prompt:
> regular primary prompt (`k=i` or default),
> right-side prompts (`k=r`), or prompts for continuation lines
> (`k=c` or `k=s`).

> Applicable options: `k` (kind).

`OSC "133;B"` _options_ `"\007"`
> End of prompt and start of user input, terminated
> by a `OSC "133;C"` or another prompt (`OSC "133;P"`).

`OSC "133;I"` _options_ `"\007"`
> End of prompt and start of user input, terminated
> by end-of-line.

`OSC "133;C"` _options_ `"\007"`

> End of input, and start of output.

`OSC "133;D"` [`";"` _exit-code_ _options ]`"\007"`

> End of current command.

> The _exit-code_ need not be specified if  if there are no _options_,
> or if the command was cancelled (no `OSC "133;C"`),
> such as by typing an interrupt/cancel character
> (typically ctrl-C) during line-editing.
> Otherwise, it must be an integer code, where `0` means the command
> succeeded, and other values indicate failure.
> In additing to the _exit-code_ there may be an `err=` option,
> which non-legacy terminals should give precedence to.
> The `err=_value_` option is more general: an empty string is success,
> and any non-empty _value_ (which need not be an integer) is an error code.
> So to indicate success both ways you could send
> `OSC "133;D;0;err=\007"`, though `OSC "133;D;0\007" is shorter.

> These rules are for backward compatibility with iTerm2.

> Applicable options: `aid`, `err`.

## General options syntax

Options have the general format:

> _options_ ::= (`";"` _option_)*

In general, an _option_ can be any string of characters not including control characters (value less than 32) or semicolon.  Usually the will have the form of a _named-option_:

_named-option_ ::= _option-name_ `"="` _value_

A terminal must ignore an _option_ whose _option-name_ it doesn't recognize.

**Issue**: Alternative specification: If a terminal doesn't recognize an _option-name_ it should ignore it _and_ any subsequernt options.

**Issue**: Maybe allow _value_ to be a quoted string, with C-style escapes?

## Standard options

`aid=` _value_
> The current _application identifier_.

`err=` _value_
> Only applicable to a `OSC "133;Z"`command.
> Specifies a string value that reports an error code from the command.
> Note that any non-empty _value_ should be displayed as an error, even 0.
> A non-error is indicated by a missing `err` option or en empty _value_.
> This generality is to support command processors that don't
> follow the standard Unix convention of 0 for success.
> For a Unix-like shells, on success (zero exit code) there should be
> no `err` option or an empty `err` option;
> on failure (non-zero exit code), _value) should be the exit code.

`cl=` _value_

> This option is defined for `A` or `N` commands.

> This option is a request from the application to the terminal to
> translate clicks in the input area to cursor movement.
> The terminal is free to ignore this option, or to treat
> any specified _value_ as `line`.
> See separate discussion below.
> If implemented. it provides a more natural user experience,
> especially for beginners, who are often surprised when a mouse 
> click doesn't move the cursor.

> The _value_ can be one of `line`, `m`, `v`, or `w`,
> specifying what kind of cursor key sequences are handled by the application.
> The value `line` allows motion within  single input line, using
> using standard left/right arrow escape sequences
> (or overridden by `move-keys`).  Only a single left/right sequence
> should be emitted for double-width characters.
> The value `m` (for "multiple") allows movement between different lines
> (in the same group), but only using left/right arrow escape sequences.
> The values `v` or `w` is like `m` but cursor up/down should
> be used.
> Using `w` specifies that that there are no spurious spaces
> (written by the application though not typed by the user)
> at the end of the line, _and_ that the application editor handles
> "smart vertical movement". (This means that moving 2 lines up from
> character position 20, where the intermediate line is 15 characters wide
> and the destination line is 18 characters wide, ends at position 18.)
> If `v` is specified instead of `w` then the terminal
> should be more conservative when moving between lines:
> It should move the cursor left to the start of line, then emit the needed
> up or down sequences, then move the cursor right to the clicked destination.

> To support extensions, _value_ is allowed to be a comma-separated list.

`move-keys=` _left_ `","` _right_ [`","` _up_, `","` _down_]
> Specify key sequences to use for `click-move` mode when
> translating clicks to cursor motion.
> The defaults are the standard arrow-key sequences: `CSI D` etc.
> (_Note:_ this is an example where allowing quoted strings would be useful.)

`k=` _prompt_kind_
> Specify the kind of prompt sequence.
> A normal left first-line prompt has kind `i` (initial), which is the default.
> Prompts for continuation lines have kind `c` (continuation),
> or kind `s` (secondary).
> The difference between `c` and `s` is that `c` allows
> the user to "go back" and edit previous lines, while `s` does not.
> A right-aligned prompt has kind `r` (right).
> When reflowing lines with a `r` prompt, a terminal may optionally
> adjust spacing so the right prompt stays at the right end of the line.

## Input lines

Each input section consists of one or more input lines,
The structure of an input line is an optional initial prompt,
followed by a possibly-empty user input area,
followed by "background area", followed by an optional final prompt.

### End of input / start of output

The `OSC "133;C"` command can be used to explicitly end
the input area and begin the output area.  However, some applications
don't provide a convenient way to emit that command.
That is why we also specify an _implicit_ way to end the input area
at the end of the line.
In the case of  multiple input lines: If the cursor is on a fresh (empty) line
and we see either `OSC "133;P"` or `OSC "133;I"` then this is the
start of a continuation input line.  If we see anything else,
it is the start of the output area (or end of command).

### Input vs background

The blank area between the input area and the right prompt (if any)
or the end of line is the _background area_.  There may be actual
spaces (explicitly typed by the user) at the end of the input
are, but it is desirable for the terminal to be able distinguish these
from the background:

- Selected text should include explicit spaces but not background space.
- You may want a distinct styling for actual input text.
- A terminal may want to handle mouse clicks in the input area
  different from those in the background.

A terminal can manage this distinction using
a special logical style for text in the input area.
Input text echoed by the application should get this style.
Care must be taken when deleting characters: All deletion should be
done with either Delete Character (`CSI P`) or Erase in Line (`CSI K`),
but _not_ by overwriting with a space.  If the application
needs to write a final (right) prompt, it should move the
cursor in position using Cursor Forward (`CSI C`), not by writing spaces.

### Prompts
An application should emit each prompt string as a single
undivided sequence, not containing cursor motion
(including no carriage return or line-feed).
A prompt starts with a `'OSC 133;A'` or `'OSC 133;N'` sequence
(for the initial prompt), or `'OSC 133;P'` (for subsequent prompts).
It must end with `'OSC 133;B'` or `'OSC 133;I'` or
end-of-line (only for final/right prompts).

Initial indentation (such as emitted by `fish`) should
treated and delimited as a (blank) prompt. Alternatively, it can
be handled "background", which means it should be "written"
using cursor motion (`CSI C`), not spaces.

If there is a right prompt in an input line that wraps around (logical
line wider than screen width), the prompt should be written at
the end of the final screen line, not the first.  This allows
line-wrap indicators to be correct.

**Issue**: Possibly add a way to mark indentation as distinct from prompts.

### Canceling input

If input is cancelled (such as by typing ctrl-c),
then the application should avoid emitting a `OSC 133;C` command,
and instead just emit a `OSC 133;D` command.
It is suggested it specify a `err=CANCEL` option, but it is not required.

### Window re-size

Many terminals re-flow long (wrapped) lines when the containing
window changes size.  It is recommended that terminal should _not_
re-flow the current input area when the cursor is in the area.
The reason is that most input editors will repaint the input area
on windows resize, and inconsistencies are possible if both
terminal and application try to re-flow, as there is no easy synchronization.

## Cursor motion from mouse clicks

People new to shells and similar REPLs usually expect that they can move
the input cursor using a mouse click, and are surprised when it doesn't work.
Experienced shell users also sometimes want this ability,
for example making an edit to a long command.  It is possible for
an application (or input library) to implement this using xterm-style
mouse support, but there are disadvantages doing that:

- There is some per-application implementation effort to add mouse support.
  This may be infeasible if you don't have source-code access or
  the skills to modify the application.
- The xterm mouse support is too coarse grain.  If an application
  requests mouse reporting, it disables the default mouse behavior
  for selection-by-drag and for context menus (third-button click).
  It is difficult for an application to re-enable that functionality.

However, if an application supports cursor movement using
the arrow keys (or some other key sequence), then it is easy for the
terminal to translate a mouse click to the appropriate key sequence.

Moving the cursor within the current input line is requested
by the `cl=line` option.  If the terminal supports this
feature, it should be activated when the user clicks
in an input line containing the cursor,
using a button-1 (left) click and no modifier keys.
(If the click is in the left prompt, it is treated at the first character
of the input area; if it is to the right of input area (in the background area or right prompt) it is treated as just after the input area.)
The terminal calculates the character position of the click
minus the character position of the cursor.  (Character position is
like column position, but double-width characters only count one.)
If the count is positive, that many _right_ sequences (by default `CSI C`)
are sent; if negative, that many _left_  sequences (by default `CSI D`).

## Omitted-newline sequence

Both fish and zsh emit after a command finishes special strings
to deal with output that does not end with a newline.
These strings have no effect for output that (correctly) ends with a newline,
but otherwise they append a special marker, and add the missing newline.

Such "omitted-newline" sequences can be emitted before or after the
end-of-commmand sequence (`D` or `Z`), but must be emitted before
the next start-of-command seuqnec (`A` or `N`).

Note that re-flow due to making the window narrower may
cause an extra undesired blank line.
A _fresh-line_ sequence (`L`) does not have that downside, but
it doesn't have a way to specify a marker.

# Resources

## Bash shell

If you use [the Bourne-Again SHell (bash)](https://www.gnu.org/software/bash/)
`source` [this initialization script](./prompts-data/shell-integration.bash).
It depends on the [bash-preexec.sh script](./prompts-data/bash-preexec.sh)
from [this site](https://github.com/rcaloras/bash-preexec),
which you should `source` first.

Bash has very limited support for multi-line input editing.

## Fish shell

If you use [fish shell](https://fishshell.com/) use
[this initialization script](./prompts-data/shell-integration.fish).

This works best with [with GitHub master of fish](https://github.com/fish-shell/fish-shell/).

## Zsh shell

If you use [Zsh](https://www.zsh.org/) use
[this initialization script](./prompts-data/shell-integration.zsh).

Zsh may leave spurious spaces at the end of a line that is shortened -
see [this bug report](http://www.zsh.org/mla/workers/2019/msg00247.html).

## DomTerm terminal

[DomTerm](https://domterm.org) implements the latest version of this specification.

# Open Questions

See the paragraphs starting with **Issue:**.

Also need to verify that the above scripts don't do weird things for
old ITerm2 versions.
