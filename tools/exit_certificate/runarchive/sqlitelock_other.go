//go:build !unix

package runarchive

// sqliteOpenElsewhere cannot tell whether another process has the database open on this platform: it reports
// it as not open.
func sqliteOpenElsewhere(string) (bool, error) {
	return false, nil
}
