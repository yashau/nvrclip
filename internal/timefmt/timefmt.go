package timefmt

import "time"

const DahuaLayout = "2006-01-02 15:04:05"

func Dahua(t time.Time) string {
	return t.Format(DahuaLayout)
}
