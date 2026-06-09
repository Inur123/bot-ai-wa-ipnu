package timeutil

import "time"

const zoneName = "Asia/Jakarta"

// Location mengembalikan timezone WIB (Asia/Jakarta).
func Location() *time.Location {
	loc, err := time.LoadLocation(zoneName)
	if err != nil {
		return time.FixedZone("WIB", 7*60*60)
	}
	return loc
}

// Now mengembalikan waktu saat ini dalam timezone WIB.
func Now() time.Time {
	return time.Now().In(Location())
}

// InWIB mengubah waktu apa pun ke timezone WIB.
func InWIB(t time.Time) time.Time {
	return t.In(Location())
}
