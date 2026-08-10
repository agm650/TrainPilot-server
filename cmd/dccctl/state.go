package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
)

type savedLease struct {
	ID string `json:"id"`
}

type savedProfile struct {
	AccessToken      string                `json:"accessToken"`
	RefreshToken     string                `json:"refreshToken"`
	AccessExpiresAt  time.Time             `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time             `json:"refreshExpiresAt"`
	SessionID        string                `json:"sessionId"`
	Leases           map[string]savedLease `json:"leases,omitempty"`
}

type cliState struct {
	Profiles map[string]*savedProfile `json:"profiles"`
}

func defaultStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dccctl", "state.json"), nil
}

func loadState(path string) (*cliState, error) {
	s := &cliState{Profiles: make(map[string]*savedProfile)}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Profiles == nil {
		s.Profiles = make(map[string]*savedProfile)
	}
	return s, nil
}

func (s *cliState) profile(server, username string) *savedProfile {
	key := server + "|" + username
	p := s.Profiles[key]
	if p == nil {
		p = &savedProfile{Leases: make(map[string]savedLease)}
		s.Profiles[key] = p
	}
	if p.Leases == nil {
		p.Leases = make(map[string]savedLease)
	}
	return p
}

func (p *savedProfile) setTokens(pair service.TokenPair) {
	p.AccessToken = pair.AccessToken
	p.RefreshToken = pair.RefreshToken
	p.AccessExpiresAt = pair.AccessExpiresAt
	p.RefreshExpiresAt = pair.RefreshExpiresAt
	p.SessionID = pair.SessionID
}

func (p *savedProfile) setLease(l model.ControlLease) {
	p.Leases[l.LocomotiveID] = savedLease{ID: l.ID}
}

func saveState(path string, s *cliState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
