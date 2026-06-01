package thread

import "testing"

// TestResolvedProviderModelHotReload verifies that resolvedProviderModel prefers
// the hot-reload DefaultModelFn over the startup ProviderName/ModelName snapshot,
// so a config.yaml edit to thread.provider/modelType is reflected without a
// daemon restart (the stale-label bug this fixes). With no agent specialty,
// resolvedModelConfig returns nil and the default path is taken.
func TestResolvedProviderModelHotReload(t *testing.T) {
	newThread := func(fn func() (string, string)) *Thread {
		return &Thread{
			mgr: NewManager(&ThreadConfig{
				ProviderName:   "stale-provider", // startup snapshot
				ModelName:      "stale-model",
				DefaultModelFn: fn,
			}),
		}
	}

	t.Run("fn value wins over snapshot", func(t *testing.T) {
		th := newThread(func() (string, string) { return "deepseek", "deepseek-v4-pro" })
		p, m := th.resolvedProviderModel()
		if p != "deepseek" || m != "deepseek-v4-pro" {
			t.Errorf("got %q/%q, want deepseek/deepseek-v4-pro (hot-reload value)", p, m)
		}
	})

	t.Run("nil fn falls back to snapshot", func(t *testing.T) {
		th := newThread(nil)
		p, m := th.resolvedProviderModel()
		if p != "stale-provider" || m != "stale-model" {
			t.Errorf("got %q/%q, want stale-provider/stale-model (snapshot)", p, m)
		}
	})

	t.Run("empty fn provider falls back to snapshot", func(t *testing.T) {
		th := newThread(func() (string, string) { return "", "" })
		p, m := th.resolvedProviderModel()
		if p != "stale-provider" || m != "stale-model" {
			t.Errorf("got %q/%q, want stale-provider/stale-model (snapshot)", p, m)
		}
	})
}
