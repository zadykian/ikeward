package routing

import (
	"errors"
	"fmt"

	"github.com/zadykian/ikeward/internal/files"
)

// ErrSettings starts ReadSettings' error. install writes files.SettingsFile, and nothing else mends
// it: a restart of strongSwan does not, as the watchdog's unchanged says.
var ErrSettings = errors.New("cannot read " + files.SettingsFile)

// ReadSettings returns what files.SettingsFile holds. A file that is missing, or that
// files.ParseSettings refuses, fails it with an error that wraps ErrSettings.
func ReadSettings(f Reader) (files.Settings, error) {
	data, _, err := f.Read(files.SettingsFile)
	if err != nil {
		return files.Settings{}, fmt.Errorf("%w, which install writes: %w", ErrSettings, err)
	}
	s, err := files.ParseSettings(data)
	if err != nil {
		return files.Settings{}, fmt.Errorf("%w: %w", ErrSettings, err)
	}
	return s, nil
}
