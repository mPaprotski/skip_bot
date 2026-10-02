package sqlite

import "time"

// Civil dates use midnight UTC as a calendar marker, not as the lesson deadline.
func DateStamp(date string) int64 { t, _ := time.Parse("2006-01-02", date); return t.Unix() }
func OptionalDateStamp(date *string) interface{} {
	if date == nil {
		return nil
	}
	return DateStamp(*date)
}
