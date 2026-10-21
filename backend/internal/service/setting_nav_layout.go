package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SettingKeyAdminNavLayout stores the admin sidebar layout (visibility, custom
// groups and order) as an opaque JSON object. The frontend owns the schema and
// normalizes it against the current menu catalog, so unknown or removed menu
// entries are tolerated.
const SettingKeyAdminNavLayout = "admin_nav_layout"

// MaxAdminNavLayoutBytes caps the stored layout document.
const MaxAdminNavLayoutBytes = 64 * 1024

// ErrInvalidAdminNavLayout is returned when the layout is not a JSON object or too large.
var ErrInvalidAdminNavLayout = errors.New("invalid admin nav layout")

// GetAdminNavLayout returns the stored layout JSON, or "{}" when none is saved.
func (s *SettingService) GetAdminNavLayout(ctx context.Context) (json.RawMessage, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAdminNavLayout)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return json.RawMessage("{}"), nil
		}
		return nil, fmt.Errorf("get admin nav layout: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" || !isJSONObject([]byte(value)) {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(value), nil
}

// SetAdminNavLayout validates and stores the layout JSON. An empty object resets to defaults.
func (s *SettingService) SetAdminNavLayout(ctx context.Context, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > MaxAdminNavLayoutBytes || !isJSONObject(raw) {
		return ErrInvalidAdminNavLayout
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ErrInvalidAdminNavLayout
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return ErrInvalidAdminNavLayout
	}
	return s.settingRepo.Set(ctx, SettingKeyAdminNavLayout, string(encoded))
}

func isJSONObject(raw []byte) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && obj != nil
}
