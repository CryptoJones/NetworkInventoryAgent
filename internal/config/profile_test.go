package config_test

import (
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_LegacyFlatSubnetsUseGlobals(t *testing.T) {
	c := &config.ScannerConfig{
		Subnets:      []string{"10.0.0.0/24", "192.168.1.0/24"},
		ScanInterval: config.Duration{Duration: 5 * time.Minute},
		Timeout:      config.Duration{Duration: 2 * time.Second},
		ProbePorts:   []int{22, 80, 443},
		DeepProbe:    true,
		EnrichARP:    true,
	}
	got, err := c.Resolve()
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, p := range got {
		assert.Equal(t, 5*time.Minute, p.ScanInterval)
		assert.Equal(t, 2*time.Second, p.Timeout)
		assert.Equal(t, []int{22, 80, 443}, p.ProbePorts)
		assert.True(t, p.DeepProbe, "global DeepProbe must propagate")
		assert.True(t, p.EnrichARP, "global EnrichARP must propagate")
	}
}

func TestResolve_ProfileOverridesWin(t *testing.T) {
	c := &config.ScannerConfig{
		Profiles: []config.SubnetProfile{
			{
				Subnet:       "10.0.0.0/24",
				ScanInterval: config.Duration{Duration: time.Hour},
				ProbePorts:   []int{443},
				DeepProbe:    config.True(),
			},
			{
				Subnet: "192.168.1.0/24",
				// Inherits everything from globals.
			},
		},
		ScanInterval: config.Duration{Duration: 5 * time.Minute},
		Timeout:      config.Duration{Duration: 2 * time.Second},
		ProbePorts:   []int{22, 80, 443},
		DeepProbe:    false,
	}
	got, err := c.Resolve()
	require.NoError(t, err)
	require.Len(t, got, 2)

	// First profile overrides.
	assert.Equal(t, time.Hour, got[0].ScanInterval)
	assert.Equal(t, []int{443}, got[0].ProbePorts)
	assert.True(t, got[0].DeepProbe)
	assert.Equal(t, 2*time.Second, got[0].Timeout, "Timeout inherits from global")

	// Second profile inherits everything.
	assert.Equal(t, 5*time.Minute, got[1].ScanInterval)
	assert.Equal(t, []int{22, 80, 443}, got[1].ProbePorts)
	assert.False(t, got[1].DeepProbe)
}

func TestResolve_ExplicitFalseBeatsGlobalTrue(t *testing.T) {
	c := &config.ScannerConfig{
		Profiles: []config.SubnetProfile{
			{Subnet: "10.0.0.0/24", DeepProbe: config.False()},
		},
		ScanInterval: config.Duration{Duration: 5 * time.Minute},
		DeepProbe:    true, // global ON, profile must turn it OFF
	}
	got, err := c.Resolve()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].DeepProbe, "pointer-bool False() must override the global True")
}

func TestResolve_SubnetsAndProfilesMutuallyExclusive(t *testing.T) {
	c := &config.ScannerConfig{
		Subnets:  []string{"10.0.0.0/24"},
		Profiles: []config.SubnetProfile{{Subnet: "192.168.1.0/24"}},
	}
	_, err := c.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestResolve_DuplicateSubnetRejected(t *testing.T) {
	c := &config.ScannerConfig{
		Profiles: []config.SubnetProfile{
			{Subnet: "10.0.0.0/24"},
			{Subnet: "10.0.0.0/24"},
		},
	}
	_, err := c.Resolve()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listed twice")
}

func TestResolve_EmptySubnetRejected(t *testing.T) {
	c := &config.ScannerConfig{
		Profiles: []config.SubnetProfile{{Subnet: ""}},
	}
	_, err := c.Resolve()
	require.Error(t, err)
}

func TestResolve_NoSubnetsOrProfilesIsValid(t *testing.T) {
	c := &config.ScannerConfig{
		ScanInterval: config.Duration{Duration: 5 * time.Minute},
	}
	got, err := c.Resolve()
	require.NoError(t, err)
	assert.Empty(t, got, "no-config deployment is allowed (watchdog-only mode)")
}
