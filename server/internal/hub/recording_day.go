package hub

import "github.com/nwverse/persona/server/internal/assistantwire"

// featureRecordingDay: the default manager takes all-day recordings and writes their daily recap (#4741). The
// recordings' routes go to it, so its hello alone decides, as for meeting_attendance; an extension's report cannot add
// the feature (features).
const featureRecordingDay = assistantwire.CapabilityRecordingDay

// recordingDayFeatures is the feature while the default manager is connected and named the capability in its hello;
// nothing otherwise, so a phone uploads no day recording to an older manager, which refuses the kind.
func (h *Hub) recordingDayFeatures() []string {
	if !h.defaultManagerNames(assistantwire.CapabilityRecordingDay) {
		return nil
	}
	return []string{featureRecordingDay}
}
