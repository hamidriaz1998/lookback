package index

import "golang.org/x/sys/windows"

const (
	attrOffline          = 0x00001000 // FILE_ATTRIBUTE_OFFLINE: the data is not on this computer
	attrRecallOnOpen     = 0x00040000 // FILE_ATTRIBUTE_RECALL_ON_OPEN: a cloud placeholder that opening downloads
	attrRecallOnDataRead = 0x00400000 // FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS: an online-only OneDrive file

	// excludedAttrs are the attributes that exclude a file or folder by default (SPEC "Exclusions on Windows",
	// Q33 and Q36): hidden and system entries (the recycle bin, System Volume Information, most of AppData), and
	// online-only files, which reading would download.
	excludedAttrs = windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_SYSTEM |
		attrOffline | attrRecallOnOpen | attrRecallOnDataRead
)

// isHidden reports whether the file or folder at p has an attribute that excludes it. It reads the attributes with
// GetFileAttributes and never opens the file, because opening an online-only placeholder downloads it. A path that
// cannot be read is not excluded: the walk reports the error itself.
func isHidden(p string) bool {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(name)
	return err == nil && attrs&excludedAttrs != 0
}
