// Package watch reports changes under a folder tree. On Linux it uses inotify and watches every
// directory under the root (SPEC section 13, Q5). Other systems get a stub that returns errors.ErrUnsupported.
package watch
