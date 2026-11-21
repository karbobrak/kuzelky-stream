// Package fsutil has file helpers shared by the stores.
package fsutil

import (
	"os"
	"time"
)

// Replace moves tmp over path. On Windows the rename fails with "Access is denied" while another
// process (the Google Drive backup, an antivirus, the search indexer) has path open for a moment,
// so it is retried for about a second before giving up.
func Replace(tmp, path string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(10+i*5) * time.Millisecond)
	}
	return err
}
