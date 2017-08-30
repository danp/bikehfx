package configutil

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	exception "github.com/blendlabs/go-exception"
	util "github.com/blendlabs/go-util"
	"github.com/blendlabs/go-util/env"
	yaml "gopkg.in/yaml.v2"
)

const (
	// EnvVarConfigPath is the env var for configs.
	EnvVarConfigPath = "CONFIG_PATH"
	// EnvVarServiceName is the env var for the service name.
	EnvVarServiceName = "SERVICE_NAME"
	// EnvVarServiceEnv is the env var for the service environment.
	EnvVarServiceEnv = "SERVICE_ENV"
	// DefaultConfigPathTemplate is the default path for configs parameterized by the `serviceName`.
	DefaultConfigPathTemplate = "/var/run/secrets/${serviceName}/config"

	// DefaultConfigPath is the default path for configs.
	DefaultConfigPath = "/var/run/secrets/config/config.yml"

	// ExtensionJSON is a file extension.
	ExtensionJSON = ".json"
	// ExtensionYAML is a file extension.
	ExtensionYAML = ".yaml"
	// ExtensionYML is a file extension.
	ExtensionYML = ".yml"
)

// Vars is a loose type alias to map[string]string
type Vars = map[string]string

// Any is a loose type alias to interface{}.
type Any = interface{}

// Path returns the config path.
func Path(defaults ...string) string {
	if env.Env().Has(EnvVarConfigPath) {
		return env.Env().String(EnvVarConfigPath)
	}
	if len(defaults) > 0 {
		return defaults[0]
	}
	if env.Env().Has(EnvVarServiceName) {
		return util.String.Tokenize(DefaultConfigPathTemplate, Vars{"serviceName": env.Env().String(EnvVarServiceName)})
	}
	return DefaultConfigPath
}

// Deserialize deserializes a config.
func Deserialize(ext string, r io.Reader, ref Any) error {
	switch strings.ToLower(ext) {
	case ExtensionJSON:
		return exception.Wrap(json.NewDecoder(r).Decode(ref))
	case ExtensionYAML, ExtensionYML:
		contents, err := ioutil.ReadAll(r)
		if err != nil {
			return exception.Wrap(err)
		}
		return exception.Wrap(yaml.Unmarshal(contents, ref))
	default:
		return exception.Wrap(json.NewDecoder(r).Decode(ref))
	}
}

// Read reads a config from a default path (or inferred path from the environment).
func Read(ref Any, defaultPath ...string) error {
	return ReadFromPath(ref, Path(defaultPath...))
}

// ReadFromPath reads a config from a given path.
func ReadFromPath(ref Any, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return exception.Wrap(err)
	}
	defer f.Close()
	err = Deserialize(filepath.Ext(path), f, ref)
	if err != nil {
		return err
	}

	// also read the env into the config
	return env.Env().ReadInto(ref)
}
