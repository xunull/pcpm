//go:build !darwin

package watch

import "os/exec"

// newTrafficCommand reports that there is nothing to run.
//
// Linux has no unprivileged per-process byte source of the kind macOS exposes:
// every tool that does this there — nethogs, bandwhich, netdata — needs
// CAP_NET_RAW, CAP_NET_ADMIN or root, because they have to count the packets
// themselves. pcpm will not ask for that, so traffic is simply absent here
// until a platform-specific implementation is written.
func newTrafficCommand(int) *exec.Cmd { return nil }
