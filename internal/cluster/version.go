package cluster

import (
	"fmt"
	"os"

	"github.com/taigrr/crush/internal/update"
)

// ProtocolVersion is the node-to-node wire protocol version. Bump it on
// any incompatible change to the cluster RPCs.
const ProtocolVersion = 1

const (
	headerVersion  = "X-Crush-Version"
	headerProtocol = "X-Crush-Protocol"
)

// CheckCompatible reports whether a peer running remoteVersion and
// remoteProtocol may interoperate with this node. Clustering requires
// identical releases; dev builds are refused unless
// CRUSH_CLUSTER_ALLOW_DEV is set.
func CheckCompatible(localVersion, remoteVersion string, remoteProtocol int) error {
	if remoteProtocol != ProtocolVersion {
		return fmt.Errorf("cluster protocol mismatch: local %d, remote %d (run `crush cluster upgrade`)", ProtocolVersion, remoteProtocol)
	}
	if os.Getenv("CRUSH_CLUSTER_ALLOW_DEV") == "" {
		if isDevVersion(localVersion) || isDevVersion(remoteVersion) {
			return fmt.Errorf("clustering requires a released build (local %q, remote %q); install with `crush upgrade`", localVersion, remoteVersion)
		}
	}
	if localVersion != remoteVersion {
		return fmt.Errorf("version mismatch: local %s, remote %s (run `crush cluster upgrade`)", localVersion, remoteVersion)
	}
	return nil
}

func isDevVersion(v string) bool {
	return v == "" || v == "(devel)" || update.Info{Current: v}.IsDevelopment()
}
