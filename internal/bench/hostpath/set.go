package hostpath

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/crispebble/orbit-mem/internal/config"
)

// owned are the settings the path itself sets from the case file and the harness (the product map, the
// time limits), so an override of them would be lost.
var owned = map[string]bool{"products": true, "hostTimeouts": true}

// CheckSet says whether set could be applied: every key is a setting the settings file knows and every
// value passes the check the file does (config.Config.Validate).
func CheckSet(set map[string]string) error {
	_, err := applySet(config.Default(), set)
	return err
}

// applySet puts the overrides on cfg. A key is the name in the settings file; the value is read as the
// file would write it (a JSON number or boolean, else text). The result is checked as a settings file
// is, and recall and inject move together as `orbit-mem config set` moves them.
func applySet(cfg config.Config, set map[string]string) (config.Config, error) {
	if len(set) == 0 {
		return cfg, nil
	}
	known := settingKeys()
	obj := map[string]json.RawMessage{}
	for _, k := range sortedKeys(set) {
		if owned[k] {
			return cfg, fmt.Errorf("hostpath: setting %q is set by the case file, not overridable", k)
		}
		if !known[k] {
			return cfg, fmt.Errorf("hostpath: unknown setting %q", k)
		}
		v := set[k]
		raw := json.RawMessage(v)
		if !json.Valid(raw) {
			raw, _ = json.Marshal(v)
		}
		obj[k] = raw
	}
	b, _ := json.Marshal(obj)
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("hostpath: setting value: %w", err)
	}
	_, hasRecall := set["recall"]
	_, hasInject := set["inject"]
	switch {
	case hasRecall:
		cfg.Inject = config.InjectOfRecall(cfg.Recall)
	case hasInject:
		switch cfg.Inject {
		case config.InjectFull:
			cfg.Recall = config.RecallAuto
		case config.InjectRules:
			cfg.Recall = config.RecallOnDemand
		case config.InjectOff:
			cfg.Recall = config.RecallOff
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// settingKeys are the JSON keys of the fields of config.Config.
func settingKeys() map[string]bool {
	known := map[string]bool{}
	t := reflect.TypeOf(config.Config{})
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			known[name] = true
		}
	}
	return known
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
