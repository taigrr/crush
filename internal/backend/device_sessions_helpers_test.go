package backend

import (
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
)

func messageIDs(msgs []message.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

func overviewIDs(s []proto.DeviceSessionOverview) []string {
	out := make([]string, 0, len(s))
	for _, o := range s {
		out = append(out, o.ID)
	}
	return out
}

func proto0() proto.NodeSearchHistoryParams {
	return proto.NodeSearchHistoryParams{Query: "nothing matches this"}
}
