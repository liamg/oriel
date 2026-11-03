package term

// KeyEvent is a key going down, repeating or coming up. Key is the physical key; Text is what the keyboard layout
// produced (e.g. "a", "A", "é"), and Unshifted the key's character without modifiers, for protocols that report it.
type KeyEvent struct {
	Key       Key
	Mods      Mods
	Action    KeyAction
	Text      string
	Unshifted rune
}

type KeyAction uint8

const (
	KeyRelease KeyAction = iota
	KeyPress
	KeyRepeat
)

// Mods are the modifiers held with a key, and lock states.
type Mods uint16

const (
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
	ModCapsLock
	ModNumLock
)

// Key is a physical key, named as the W3C UI Events KeyboardEvent code names it. The values are libghostty-vt's.
type Key uint16

const (
	KeyUnidentified Key = iota
	KeyBackquote
	KeyBackslash
	KeyBracketLeft
	KeyBracketRight
	KeyComma
	KeyDigit0
	KeyDigit1
	KeyDigit2
	KeyDigit3
	KeyDigit4
	KeyDigit5
	KeyDigit6
	KeyDigit7
	KeyDigit8
	KeyDigit9
	KeyEqual
	KeyIntlBackslash
	KeyIntlRo
	KeyIntlYen
	KeyA
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
	KeyMinus
	KeyPeriod
	KeyQuote
	KeySemicolon
	KeySlash
	KeyAltLeft
	KeyAltRight
	KeyBackspace
	KeyCapsLock
	KeyContextMenu
	KeyControlLeft
	KeyControlRight
	KeyEnter
	KeyMetaLeft
	KeyMetaRight
	KeyShiftLeft
	KeyShiftRight
	KeySpace
	KeyTab
	KeyConvert
	KeyKanaMode
	KeyNonConvert
	KeyDelete
	KeyEnd
	KeyHelp
	KeyHome
	KeyInsert
	KeyPageDown
	KeyPageUp
	KeyArrowDown
	KeyArrowLeft
	KeyArrowRight
	KeyArrowUp
	KeyNumLock
	KeyNumpad0
	KeyNumpad1
	KeyNumpad2
	KeyNumpad3
	KeyNumpad4
	KeyNumpad5
	KeyNumpad6
	KeyNumpad7
	KeyNumpad8
	KeyNumpad9
	KeyNumpadAdd
	KeyNumpadBackspace
	KeyNumpadClear
	KeyNumpadClearEntry
	KeyNumpadComma
	KeyNumpadDecimal
	KeyNumpadDivide
	KeyNumpadEnter
	KeyNumpadEqual
	KeyNumpadMemoryAdd
	KeyNumpadMemoryClear
	KeyNumpadMemoryRecall
	KeyNumpadMemoryStore
	KeyNumpadMemorySubtract
	KeyNumpadMultiply
	KeyNumpadParenLeft
	KeyNumpadParenRight
	KeyNumpadSubtract
	KeyNumpadSeparator
	KeyNumpadUp
	KeyNumpadDown
	KeyNumpadRight
	KeyNumpadLeft
	KeyNumpadBegin
	KeyNumpadHome
	KeyNumpadEnd
	KeyNumpadInsert
	KeyNumpadDelete
	KeyNumpadPageUp
	KeyNumpadPageDown
	KeyEscape
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
	KeyF21
	KeyF22
	KeyF23
	KeyF24
	KeyF25
	KeyFn
	KeyFnLock
	KeyPrintScreen
	KeyScrollLock
	KeyPause
	KeyBrowserBack
	KeyBrowserFavorites
	KeyBrowserForward
	KeyBrowserHome
	KeyBrowserRefresh
	KeyBrowserSearch
	KeyBrowserStop
	KeyEject
	KeyLaunchApp1
	KeyLaunchApp2
	KeyLaunchMail
	KeyMediaPlayPause
	KeyMediaSelect
	KeyMediaStop
	KeyMediaTrackNext
	KeyMediaTrackPrevious
	KeyPower
	KeySleep
	KeyAudioVolumeDown
	KeyAudioVolumeMute
	KeyAudioVolumeUp
	KeyWakeUp
	KeyCopy
	KeyCut
	KeyPaste
)
