package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	SeatSnapshotInvalid          = "SEAT_SNAPSHOT_INVALID"
	SeatAgentUnknown             = "SEAT_AGENT_UNKNOWN"
	SeatStartupInvalid           = "SEAT_STARTUP_INVALID"
	SeatToolAdapterUnimplemented = "SEAT_TOOL_ADAPTER_UNIMPLEMENTED"
)

// SeatError deliberately excludes raw decoder errors, paths and configuration
// values: a private snapshot can contain prompts and credentials.
type SeatError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *SeatError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// LoadStartupSeat reads only the immutable private artifact. S4.4 accepts initial
// startup identities only; live snapshots and later generations require a future
// authenticated coordinator protocol. It performs no executable lookup or launch.
func LoadStartupSeat(path, id string) (SeatSnapshot, error) {
	if !seatID.MatchString(id) {
		return SeatSnapshot{}, &SeatError{SeatAgentUnknown, "agent", "invalid or unknown agent identity"}
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return SeatSnapshot{}, &SeatError{SeatSnapshotInvalid, "snapshot", "snapshot must be an absolute private artifact path"}
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		return SeatSnapshot{}, &SeatError{SeatSnapshotInvalid, "snapshot", "cannot read a valid private runtime snapshot"}
	}
	var selected SeatSnapshot
	for _, seat := range snapshot.Seats {
		if seat.Status != Starting || seat.Generation != 1 || seat.Native != (NativeBinding{}) {
			return SeatSnapshot{}, &SeatError{SeatStartupInvalid, "snapshot.seats", "startup requires STARTING seats at generation 1 with no native binding"}
		}
		if seat.AgentID == id {
			selected = seat
		}
	}
	if selected.AgentID == "" {
		return SeatSnapshot{}, &SeatError{SeatAgentUnknown, "agent", "invalid or unknown agent identity"}
	}
	return selected, nil
}
