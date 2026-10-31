package installer

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestVPAAdoptExistingCRDs_LabelsLeftoverCRDs(t *testing.T) {
	sh := &fakeShell{}
	NewVPA(&config.Config{}, sh).adoptExistingCRDs()

	var annotated int
	for _, c := range sh.calls {
		if strings.Contains(c, "meta.helm.sh/release-name=vpa") {
			annotated++
		}
	}
	assert.Equal(t, len(vpaCRDs), annotated, "every existing VPA CRD is adopted into the release")
}

func TestVPAAdoptExistingCRDs_SkipsMissingCRDs(t *testing.T) {
	notFound := errors.New("not found")
	sh := &fakeShell{
		outputs: map[string]string{"kubectl get crd": ""},
		errs:    map[string]error{"kubectl get crd": notFound},
	}
	NewVPA(&config.Config{}, sh).adoptExistingCRDs()

	for _, c := range sh.calls {
		assert.NotContains(t, c, "kubectl annotate", "fresh cluster: nothing to adopt")
	}
}
