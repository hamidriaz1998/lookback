package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// count formats n with thousands separators: 1284 becomes "1,284".
func count(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// size formats a byte count as UI guide section 7 says: one decimal place under 10 units, whole numbers above.
func size(bytes int64) string {
	const unit = 1000
	units := []string{"bytes", "KB", "MB", "GB", "TB"}
	if bytes < unit {
		return fmt.Sprintf("%d bytes", bytes)
	}
	v := float64(bytes)
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d bytes", bytes)
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

// rate formats a speed in bytes per second, such as "4.1 MB/s".
func rate(bytesPerSec float64) string { return size(int64(bytesPerSec)) + "/s" }

// eta formats the time left as UI guide section 3.8 says. A negative value means the job is still estimating.
func eta(d time.Duration) string {
	switch {
	case d < 0:
		return "estimating…"
	case d < time.Minute:
		return "less than a minute"
	case d < 90*time.Minute:
		return fmt.Sprintf("about %d min", int(d.Round(time.Minute)/time.Minute))
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour).Round(time.Minute) / time.Minute)
		if m == 60 {
			h, m = h+1, 0
		}
		return fmt.Sprintf("about %d h %d min", h, m)
	}
}

// percent formats a fraction from 0 to 1 as a whole percentage, such as "42%".
func percent(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

// itoa formats an int in decimal, without separators.
func itoa(n int) string { return strconv.Itoa(n) }
