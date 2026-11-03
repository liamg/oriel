#ifndef ORIEL_ACCESS_DARWIN_H
#define ORIEL_ACCESS_DARWIN_H

#include <stdint.h>

// Roles and subroles in appkitRole's and appkitSubrole's order (access_appkit.go).
enum {
	orielAXGroup, orielAXToolbar, orielAXStaticText, orielAXButton, orielAXTextField, orielAXList, orielAXRow,
	orielAXOutline, orielAXTabGroup, orielAXRadioButton, orielAXMenu, orielAXMenuItem, orielAXSlider, orielAXImage,
	orielAXLink, orielAXCheckBox, orielAXRoleCount
};
enum { orielAXNoSubrole, orielAXTabButton, orielAXDialog, orielAXSubroleCount };

// Notifications in appkitNoteKind's order.
enum {
	orielAXNoteFocus, orielAXNoteTitle, orielAXNoteValue, orielAXNoteSelectedText, orielAXNoteLayout,
	orielAXNoteAnnounce, orielAXNoteExpanded, orielAXNoteCollapsed, orielAXNoteCount
};

// What a screen reader asks a node to do.
enum { orielAXPress, orielAXIncrement, orielAXDecrement, orielAXFocus, orielAXSetValue, orielAXSelect };

typedef struct {
	int role, subrole;
	int valueKind, number; // appkitValueKind; a number value
	int enabled, focused, selected, hasExpanded, expanded;
	int press, step, text; // it takes a press, steps up and down, holds editable text
	double x, y, w, h;     // in points from the view's top left
	char *label, *value;   // only when asked for; the caller frees them
	uint64_t parent;       // 0 is the view
	long chars;            // the value's length in UTF-16 units
	long selLoc, selLen;   // the selection, in UTF-16 units
} orielAXInfo;

#endif
