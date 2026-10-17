package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Settings struct {
	Lang        string `json:"lang"`
	RowCap      int    `json:"rowCap"`
	Variant     int    `json:"variant"`
	Rerank      bool   `json:"rerank"`
	IdleUnload  bool   `json:"idleUnload"`
	IdleMinutes int    `json:"idleMinutes"`
	Offline     bool   `json:"offline"`
}

const (
	RowCapMin          = 3
	RowCapMax          = 6
	RowCapDefault      = 4
	IdleMinutesMin     = 1
	IdleMinutesMax     = 1440
	IdleMinutesDefault = 10
)

func DefaultSettings() Settings {
	return Settings{RowCap: RowCapDefault, Rerank: true, IdleMinutes: IdleMinutesDefault, Offline: true}
}

func (s Settings) normalized() Settings {
	if s.Lang != "zh" && s.Lang != "en" {
		s.Lang = ""
	}
	if s.RowCap < RowCapMin || s.RowCap > RowCapMax {
		s.RowCap = RowCapDefault
	}
	if s.IdleMinutes < IdleMinutesMin || s.IdleMinutes > IdleMinutesMax {
		s.IdleMinutes = IdleMinutesDefault
	}
	if s.Variant != 64 && s.Variant != 96 && s.Variant != 128 {
		s.Variant = 0
	}
	return s
}

func loadSettings(path string) Settings {
	s := DefaultSettings()
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s)
	}
	return s.normalized()
}

func saveSettings(path string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
