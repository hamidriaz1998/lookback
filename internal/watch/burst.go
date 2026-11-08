package watch

import "time"

const (
	quiet   = time.Second     // a burst fires this long after its last event (SPEC section 13)
	maxWait = 5 * time.Second // ponytail: a burst also fires after this, so a busy folder cannot starve the rescan
)
