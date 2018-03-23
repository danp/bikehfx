package env

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	util "github.com/blendlabs/go-util"
)

var (
	_env     Vars
	_envLock = sync.Mutex{}
)

// Service specific constants
const (
	// VarServiceEnv is a common env var name.
	VarServiceEnv = "SERVICE_ENV"
	// VarServiceName is a common env var name.
	VarServiceName = "SERVICE_NAME"
	// VarServiceSecret is a common env var name.
	VarServiceSecret = "SERVICE_SECRET"
	// VarPort is a common env var name.
	VarPort = "PORT"
	// VarSecurePort is a common env var name.
	VarSecurePort = "SECURE_PORT"
	// VarTLSCertPath is a common env var name.
	VarTLSCertPath = "TLS_CERT_PATH"
	// VarTLSKeyPath is a common env var name.
	VarTLSKeyPath = "TLS_KEY_PATH"
	// VarTLSCert is a common env var name.
	VarTLSCert = "TLS_CERT"
	// VarTLSKey is a common env var name.
	VarTLSKey = "TLS_KEY"

	// VarPGIdleConns is a common env var name.
	VarPGIdleConns = "PG_IDLE_CONNS"
	// VarPGMaxConns is a common env var name.
	VarPGMaxConns = "PG_MAX_CONNS"

	// ServiceEnvDev is a service environment.
	ServiceEnvDev = "dev"
	// ServiceEnvCI is a service environment.
	ServiceEnvCI = "ci"
	// ServiceEnvPreprod is a service environment.
	ServiceEnvPreprod = "preprod"
	// ServiceEnvBeta is a service environment.
	ServiceEnvBeta = "beta"
	// ServiceEnvProd is a service environment.
	ServiceEnvProd = "prod"
)

// ReadIntoConstants
const (
	// FieldTagEnv is the struct tag for what environment variable to use to populate a field.
	FieldTagEnv = "env"
	// FieldFlagCSV is a field tag flag (say that 10 times fast).
	FieldFlagCSV = "csv"
	// FieldFlagBase64 is a field tag flag (say that 10 times fast).
	FieldFlagBase64 = "base64"
	// FieldFlagBytes is a field tag flag (say that 10 times fast).
	FieldFlagBytes = "bytes"
)

// Marshaler is a type that implements `ReadInto`.
type Marshaler interface {
	MarshalEnv(vars Vars) error
}

// Env returns the current env var set.
func Env() Vars {
	if _env == nil {
		_envLock.Lock()
		defer _envLock.Unlock()
		if _env == nil {
			_env = NewVarsFromEnvironment()
		}
	}
	return _env
}

// SetEnv sets the env vars.
func SetEnv(vars Vars) {
	_envLock.Lock()
	_env = vars
	_envLock.Unlock()
}

// Restore sets .Env() to the current os environment.
func Restore() {
	SetEnv(NewVarsFromEnvironment())
}

// NewVars returns a new env var set.
func NewVars() Vars {
	return Vars{}
}

// NewVarsFromEnvironment reads an EnvVar set from the environment.
func NewVarsFromEnvironment() Vars {
	vars := Vars{}
	envVars := os.Environ()
	for _, ev := range envVars {
		parts := strings.SplitN(ev, "=", 2)
		if len(parts) > 1 {
			vars[parts[0]] = parts[1]
		}
	}
	return vars
}

// Vars is a set of environment variables.
type Vars map[string]string

// Set sets a value for a key.
func (ev Vars) Set(envVar, value string) {
	ev[envVar] = value
}

// Restore resets an environment variable to it's environment value.
func (ev Vars) Restore(key string) {
	ev[key] = os.Getenv(key)
}

// Delete removes a key from the set.
func (ev Vars) Delete(key string) {
	delete(ev, key)
}

// String returns a string value for a given key, with an optional default vaule.
func (ev Vars) String(envVar string, defaults ...string) string {
	if value, hasValue := ev[envVar]; hasValue {
		return value
	}
	if len(defaults) > 0 {
		return defaults[0]
	}
	return ""
}

// CSV returns a string array for a given string var.
func (ev Vars) CSV(envVar string, defaults ...string) []string {
	if value, hasValue := ev[envVar]; hasValue && len(value) > 0 {
		return strings.Split(value, ",")
	}
	return defaults
}

// ReadFile reads a file from the env.
func (ev Vars) ReadFile(path string) error {
	return util.File.ReadByLines(path, func(line string) error {
		if len(line) == 0 {
			return nil
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) < 2 {
			return nil
		}
		ev[parts[0]] = parts[1]
		return nil
	})
}

// Bool returns a boolean value for a key, defaulting to false.
// Valid "truthy" values are `true`, `yes`, and `1`.
// Everything else is false, including `REEEEEEEEEEEEEEE`.
func (ev Vars) Bool(envVar string, defaults ...bool) bool {
	if value, hasValue := ev[envVar]; hasValue {
		if len(value) > 0 {
			return util.String.CaseInsensitiveEquals(value, "true") ||
				util.String.CaseInsensitiveEquals(value, "yes") ||
				util.String.CaseInsensitiveEquals(value, "enabled") ||
				value == "1"
		}
	}
	if len(defaults) > 0 {
		return defaults[0]
	}
	return false
}

// Int returns an integer value for a given key.
func (ev Vars) Int(envVar string, defaults ...int) (int, error) {
	if value, hasValue := ev[envVar]; hasValue {
		return strconv.Atoi(value)
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustInt returns an integer value for a given key and panics if it is malformed.
func (ev Vars) MustInt(envVar string, defaults ...int) int {
	value, err := ev.Int(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Int32 returns an integer value for a given key.
func (ev Vars) Int32(envVar string, defaults ...int32) (int32, error) {
	if value, hasValue := ev[envVar]; hasValue {
		parsedValue, err := strconv.Atoi(value)
		return int32(parsedValue), err
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustInt32 returns an integer value for a given key and panics if it is malformed.
func (ev Vars) MustInt32(envVar string, defaults ...int32) int32 {
	value, err := ev.Int32(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Int64 returns an int64 value for a given key.
func (ev Vars) Int64(envVar string, defaults ...int64) (int64, error) {
	if value, hasValue := ev[envVar]; hasValue {
		return strconv.ParseInt(value, 10, 64)
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustInt64 returns an int64 value for a given key and panics if it is malformed.
func (ev Vars) MustInt64(envVar string, defaults ...int64) int64 {
	value, err := ev.Int64(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Uint32 returns an uint32 value for a given key.
func (ev Vars) Uint32(envVar string, defaults ...uint32) (uint32, error) {
	if value, hasValue := ev[envVar]; hasValue {
		parsedValue, err := strconv.ParseUint(value, 10, 32)
		return uint32(parsedValue), err
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustUint32 returns an uint32 value for a given key and panics if it is malformed.
func (ev Vars) MustUint32(envVar string, defaults ...uint32) uint32 {
	value, err := ev.Uint32(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Uint64 returns an uint64 value for a given key.
func (ev Vars) Uint64(envVar string, defaults ...uint64) (uint64, error) {
	if value, hasValue := ev[envVar]; hasValue {
		return strconv.ParseUint(value, 10, 64)
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustUint64 returns an uint64 value for a given key and panics if it is malformed.
func (ev Vars) MustUint64(envVar string, defaults ...uint64) uint64 {
	value, err := ev.Uint64(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Float64 returns an float64 value for a given key.
func (ev Vars) Float64(envVar string, defaults ...float64) (float64, error) {
	if value, hasValue := ev[envVar]; hasValue {
		return strconv.ParseFloat(value, 64)
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustFloat64 returns an float64 value for a given key and panics if it is malformed.
func (ev Vars) MustFloat64(envVar string, defaults ...float64) float64 {
	value, err := ev.Float64(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Duration returns a duration value for a given key.
func (ev Vars) Duration(envVar string, defaults ...time.Duration) (time.Duration, error) {
	if value, hasValue := ev[envVar]; hasValue {
		return time.ParseDuration(value)
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return 0, nil
}

// MustDuration returnss a duration value for a given key and panics if malformed.
func (ev Vars) MustDuration(envVar string, defaults ...time.Duration) time.Duration {
	value, err := ev.Duration(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Bytes returns a []byte value for a given key.
func (ev Vars) Bytes(envVar string, defaults ...[]byte) []byte {
	if value, hasValue := ev[envVar]; hasValue && len(value) > 0 {
		return []byte(value)
	}
	if len(defaults) > 0 {
		return defaults[0]
	}
	return nil
}

// Base64 returns a []byte value for a given key whose value is encoded in base64.
func (ev Vars) Base64(envVar string, defaults ...[]byte) ([]byte, error) {
	if value, hasValue := ev[envVar]; hasValue && len(value) > 0 {
		return util.Base64.Decode(value)
	}
	if len(defaults) > 0 {
		return defaults[0], nil
	}
	return nil, nil
}

// MustBase64 returns a []byte value for a given key encoded with base64, and panics if malformed.
func (ev Vars) MustBase64(envVar string, defaults ...[]byte) []byte {
	value, err := ev.Base64(envVar, defaults...)
	if err != nil {
		panic(err)
	}
	return value
}

// Has returns if a key is present in the set.
func (ev Vars) Has(envVar string) bool {
	_, hasKey := ev[envVar]
	return hasKey
}

// HasAll returns if all of the given vars are present in the set.
func (ev Vars) HasAll(envVars ...string) bool {
	if len(envVars) == 0 {
		return false
	}
	for _, envVar := range envVars {
		if !ev.Has(envVar) {
			return false
		}
	}
	return true
}

// HasAny returns if any of the given vars are present in the set.
func (ev Vars) HasAny(envVars ...string) bool {
	for _, envVar := range envVars {
		if ev.Has(envVar) {
			return true
		}
	}
	return false
}

// Require enforces that a given set of environment variables are present.
func (ev Vars) Require(keys ...string) error {
	for _, key := range keys {
		if !ev.Has(key) {
			return fmt.Errorf("the following environment variables are required: `%s`", strings.Join(keys, ","))
		}
	}
	return nil
}

// Must enforces that a given set of environment variables are present and panics
// if they're not present.
func (ev Vars) Must(keys ...string) {
	for _, key := range keys {
		if !ev.Has(key) {
			panic(fmt.Sprintf("the following environment variables are required: `%s`", strings.Join(keys, ",")))
		}
	}
}

// Union returns the union of the two sets, other replacing conflicts.
func (ev Vars) Union(other Vars) Vars {
	newSet := NewVars()
	for key, value := range ev {
		newSet[key] = value
	}
	for key, value := range other {
		newSet[key] = value
	}
	return newSet
}

// Vars returns all the vars stored in the env var set.
func (ev Vars) Vars() []string {
	var envVars = make([]string, len(ev))
	var index int
	for envVar := range ev {
		envVars[index] = envVar
		index++
	}
	return envVars
}

// Raw returns a raw KEY=VALUE form of the vars.
func (ev Vars) Raw() []string {
	var raw []string
	for key, value := range ev {
		raw = append(raw, fmt.Sprintf("%s=%s", key, value))
	}
	return raw
}

// --------------------------------------------------------------------------------
// Service Specific helpers
// --------------------------------------------------------------------------------

// ServiceEnv is a common environment variable for the services environment.
// Common values include "dev", "ci", "sandbox", "preprod", "beta", and "prod".
func (ev Vars) ServiceEnv(defaults ...string) string {
	return ev.String(VarServiceEnv, defaults...)
}

// IsProduction returns if the ServiceEnv is a production environment.
func (ev Vars) IsProduction() bool {
	return ev.ServiceEnv() == ServiceEnvPreprod ||
		ev.ServiceEnv() == ServiceEnvProd
}

// IsProdlike returns if the ServiceEnv is "prodlike".
func (ev Vars) IsProdlike() bool {
	return ev.ServiceEnv() == ServiceEnvPreprod ||
		ev.ServiceEnv() == ServiceEnvBeta ||
		ev.ServiceEnv() == ServiceEnvProd
}

// ServiceName is a common environment variable for the service's name.
func (ev Vars) ServiceName(defaults ...string) string {
	return ev.String(VarServiceName, defaults...)
}

// ReadInto reads the environment into tagged fields on the `obj`.
func (ev Vars) ReadInto(obj interface{}) error {
	// check if the type implements marshaler.
	if typed, isTyped := obj.(Marshaler); isTyped {
		return typed.MarshalEnv(ev)
	}

	objMeta := util.Reflection.ReflectType(obj)
	objValue := util.Reflection.ReflectValue(obj)

	typeBool := reflect.TypeOf(false)
	typeDuration := reflect.TypeOf(time.Nanosecond)
	typeFloat32 := reflect.TypeOf(float32(1.0))
	typeFloat64 := reflect.TypeOf(float64(1.0))
	typeInt := reflect.TypeOf(1)
	typeInt8 := reflect.TypeOf(int8(1))
	typeInt16 := reflect.TypeOf(int16(1))
	typeInt32 := reflect.TypeOf(int32(1))
	typeInt64 := reflect.TypeOf(int64(1))
	typeUint := reflect.TypeOf(uint(1))
	typeUint8 := reflect.TypeOf(uint8(1))
	typeUint16 := reflect.TypeOf(uint16(1))
	typeUint32 := reflect.TypeOf(uint32(1))
	typeUint64 := reflect.TypeOf(uint64(1))
	typeUintptr := reflect.TypeOf(uintptr(1))

	var field reflect.StructField
	var tag string
	var envValue interface{}
	var err error
	var pieces []string
	var envVar string

	for x := 0; x < objMeta.NumField(); x++ {
		field = objMeta.Field(x)

		// Treat structs as nested values.
		if field.Type.Kind() == reflect.Struct {
			if err = ev.ReadInto(objValue.Field(x).Addr().Interface()); err != nil {
				return err
			}
			continue
		}

		tag = field.Tag.Get(FieldTagEnv)
		if len(tag) > 0 {
			var csv bool
			var bytes bool
			var base64 bool

			pieces = strings.Split(tag, ",")
			envVar = pieces[0]
			if len(pieces) > 1 {
				for y := 1; y < len(pieces); y++ {
					if pieces[y] == FieldFlagCSV {
						csv = true
					} else if pieces[y] == FieldFlagBase64 {
						base64 = true
					} else if pieces[y] == FieldFlagBytes {
						bytes = true
					}
				}
			}

			if !ev.Has(envVar) {
				continue
			}

			if csv {
				envValue = ev.CSV(envVar)
			} else if base64 {
				envValue, err = ev.Base64(envVar)
				if err != nil {
					return err
				}
			} else if bytes {
				envValue = ev.Bytes(envVar)
			} else {
				// infer the type.
				fieldType := util.Reflection.FollowType(field.Type)
				switch fieldType {
				case typeBool:
					if ev.Has(envVar) {
						envValue = ev.Bool(envVar)
					} else {
						continue
					}
				case typeFloat32, typeFloat64:
					envValue, err = ev.Float64(envVar)
					if err != nil {
						return err
					}
				case typeInt, typeInt8, typeInt16:
					envValue, err = ev.Int(envVar)
					if err != nil {
						return err
					}
				case typeInt32:
					envValue, err = ev.Int32(envVar)
					if err != nil {
						return err
					}
				case typeInt64:
					envValue, err = ev.Int64(envVar)
					if err != nil {
						return err
					}
				case typeUint32:
					envValue, err = ev.Uint32(envVar)
					if err != nil {
						return err
					}
				case typeUint, typeUint8, typeUint16, typeUint64, typeUintptr:
					envValue, err = ev.Uint64(envVar)
					if err != nil {
						return err
					}
				case typeDuration:
					envValue, err = ev.Duration(envVar)
					if err != nil {
						return err
					}
				default:
					envValue = ev.String(envVar)
				}
			}

			err = util.Reflection.SetValueByName(obj, field.Name, envValue)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
