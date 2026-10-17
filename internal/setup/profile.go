package setup

import (
	"os"
	"path/filepath"
	"time"
)

const (
	profileName   = "booruscope-setup-profile"
	legacyProfile = "booruscope-setup-*"
)

func setupProfileDir() string {
	return filepath.Join(os.TempDir(), profileName)
}

func removeLegacyProfiles(logf func(string, ...any)) {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), legacyProfile))
	for _, m := range matches {
		if filepath.Base(m) == profileName {
			continue
		}
		if st, err := os.Stat(m); err != nil || !st.IsDir() {
			continue
		}
		if err := os.RemoveAll(m); err != nil {
			logf("setup: could not remove old browser profile %s: %v", m, err)
		}
	}
}

func removeProfile(dir string, tries int, wait time.Duration) error {
	var err error
	for i := 0; i < tries; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(wait)
	}
	return err
}
