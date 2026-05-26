//go:build !linux

package swarmnode

import (
	"context"
	"log"
)

// startTProxy — заглушка для не-Linux платформ.
// TPROXY требует Linux (SO_TRANSPARENT, SO_ORIGINAL_DST).
func (n *Node) startTProxy(addr string) {
	log.Printf("[node] TPROXY не поддерживается на этой платформе (только Linux)")
	<-context.Background().Done()
}
