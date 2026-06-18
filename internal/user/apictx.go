package user

import (
	"context"
	"time"
)

// k8sAPITimeout bounds each Kubernetes API call so a hung apiserver fails fast
// instead of blocking indefinitely (RES-2; also clears the EH-7/EF-9
// context.TODO() items).
const k8sAPITimeout = 15 * time.Second

// apiCtx returns a context bounded by k8sAPITimeout. Callers must defer cancel().
func apiCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), k8sAPITimeout)
}
