package proto

import "encoding/json"

// TitleSource is a small provenance enum. Its JSON remains a string; unknown
// origins decode as absent so consumers never infer permission to replace a title.
// Packing it with Session's flags avoids growing every row's allocation size class.
type TitleSource uint8

const (
	TitleSourceUnknown TitleSource = iota
	TitleSourceManual
	TitleSourceAuto
	TitleSourcePrompt
	TitleSourceRuntime
)

func (s TitleSource) String() string {
	switch s {
	case TitleSourceManual:
		return "manual"
	case TitleSourceAuto:
		return "auto"
	case TitleSourcePrompt:
		return "prompt"
	case TitleSourceRuntime:
		return "runtime"
	default:
		return ""
	}
}

func ParseTitleSource(s string) TitleSource {
	switch s {
	case "manual":
		return TitleSourceManual
	case "auto":
		return TitleSourceAuto
	case "prompt":
		return TitleSourcePrompt
	case "runtime":
		return TitleSourceRuntime
	default:
		return TitleSourceUnknown
	}
}

func (s TitleSource) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }
func (s *TitleSource) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return err
	}
	*s = ParseTitleSource(name)
	return nil
}
