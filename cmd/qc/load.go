package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// envPrefix prefixes the environment variables setting flags.
const envPrefix = "QC_"

// configFlag names the flag (and, through envPrefix, the QC_CONFIG
// variable) giving the configuration file.
const configFlag = "config"

// ErrConfigFile is returned for a configuration file that cannot be read
// or holds an unknown key or an invalid value.
var ErrConfigFile = errors.New("invalid configuration file")

// loadConfig reads the flags of cmd (and inherited ones) then validates the
// result. Each flag takes, by precedence, its command-line value, its QC_*
// environment variable, its key in the configuration file (--config or
// QC_CONFIG), then its default.
//
// The environment and the file are applied to the flags themselves, before
// viper reads them (applyEnv, applyFile), rather than through viper's own
// AutomaticEnv and ReadInConfig: every value then goes through the flag's
// parser, so that an invalid one is reported with where it came from
// instead of being silently ignored or zeroed by the decoding, and only the
// keys of the running command are read, so that one file can hold the
// settings of every command.
func loadConfig(
	cmd *cobra.Command,
) (Config, error) {
	flagSets := []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags()}

	for _, flags := range flagSets {
		if err := applyEnv(flags); err != nil {
			return Config{}, err
		}
	}

	if err := applyConfigFile(cmd, flagSets); err != nil {
		return Config{}, err
	}

	v := viper.New()

	for _, flags := range flagSets {
		if err := v.BindPFlags(flags); err != nil {
			return Config{}, fmt.Errorf("bind flags: %w", err)
		}
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}

	config.Quality.precisionSet = cmd.Flags().Changed("precision")
	config = config.withGPU()

	if err := config.validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// applyEnv sets every flag not given on the command line from its QC_*
// environment variable (dashes become underscores). Values are parsed by the
// flag itself, so an invalid one is reported instead of silently ignored.
func applyEnv(
	flags *pflag.FlagSet,
) error {
	var err error

	flags.VisitAll(func(f *pflag.Flag) {
		if err != nil || f.Changed {
			return
		}

		name := envPrefix + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))

		value, ok := os.LookupEnv(name)
		if !ok {
			return
		}

		if setErr := flags.Set(f.Name, value); setErr != nil {
			err = fmt.Errorf("invalid %s: %w", name, setErr)
		}
	})

	return err
}

// applyConfigFile applies the configuration file of cmd, when one is
// given, to the flags not set yet.
func applyConfigFile(
	cmd *cobra.Command,
	flagSets []*pflag.FlagSet,
) error {
	path := ""
	if f := cmd.Flags().Lookup(configFlag); f != nil {
		path = f.Value.String()
	}

	if path == "" {
		return nil
	}

	values, err := readConfigFile(path)
	if err != nil {
		return err
	}

	known := flagNames(cmd.Root())

	for key := range values {
		if !known[key] {
			return fmt.Errorf("%w %s: unknown key %q (keys are flag names, e.g. precision)", ErrConfigFile, path, key)
		}
	}

	for _, flags := range flagSets {
		if err := applyFile(flags, values, path); err != nil {
			return err
		}
	}

	return nil
}

// readConfigFile reads a YAML, TOML or JSON file (by its extension) into
// its top-level keys.
func readConfigFile(
	path string,
) (map[string]any, error) {
	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("%w %s: %w", ErrConfigFile, path, err)
	}

	return v.AllSettings(), nil
}

// flagNames are the names of every flag of cmd and its subcommands: the
// keys a configuration file may hold.
func flagNames(
	cmd *cobra.Command,
) map[string]bool {
	names := map[string]bool{}

	var visit func(c *cobra.Command)

	visit = func(c *cobra.Command) {
		for _, flags := range []*pflag.FlagSet{c.Flags(), c.PersistentFlags()} {
			flags.VisitAll(func(f *pflag.Flag) { names[f.Name] = true })
		}

		for _, sub := range c.Commands() {
			visit(sub)
		}
	}

	visit(cmd)

	return names
}

// applyFile sets every flag not set yet (on the command line or through
// the environment) from its key in values, read from the file at path.
// Keys of flags the command does not have are left to other commands.
func applyFile(
	flags *pflag.FlagSet,
	values map[string]any,
	path string,
) error {
	var err error

	flags.VisitAll(func(f *pflag.Flag) {
		value, ok := values[f.Name]
		if err != nil || f.Changed || !ok || f.Name == configFlag {
			return
		}

		text, formatErr := flagValue(value)
		if formatErr == nil {
			formatErr = flags.Set(f.Name, text)
		}

		if formatErr != nil {
			err = fmt.Errorf("%w %s: invalid %s: %w", ErrConfigFile, path, f.Name, formatErr)
		}
	})

	return err
}

// flagValue writes a value decoded from a configuration file as a flag
// value: lists become comma-separated (quoted as the list flags parse
// them), numbers are written in full.
func flagValue(
	value any,
) (string, error) {
	list, ok := value.([]any)
	if !ok {
		return scalarValue(value)
	}

	fields := make([]string, len(list))

	for i, item := range list {
		field, err := scalarValue(item)
		if err != nil {
			return "", err
		}

		fields[i] = field
	}

	var buf bytes.Buffer

	w := csv.NewWriter(&buf)
	if err := w.Write(fields); err != nil {
		return "", fmt.Errorf("write list: %w", err)
	}

	w.Flush()

	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// ErrConfigValue is returned for a configuration value that is neither a
// scalar nor a list of scalars (a table, a date).
var ErrConfigValue = errors.New("want a string, a number, a boolean or a list of them")

// scalarValue writes a string, a boolean or a number. JSON decodes every
// number as a float64: integral ones are written without exponent, so that
// an integer flag accepts them.
func scalarValue(
	value any,
) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	}

	return "", fmt.Errorf("%w, got %T", ErrConfigValue, value)
}
