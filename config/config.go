package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/aggoracle"
	aggsendercfg "github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/prover"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/common"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/lastgersync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/pprof"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/mitchellh/mapstructure"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

const (
	// FlagCfg is the flag for cfg.
	FlagCfg = "cfg"
	// FlagComponents is the flag for components.
	FlagComponents = "components"
	// FlagSaveConfigPath is the flag to save the final configuration file
	FlagSaveConfigPath = "save-config-path"
	// FlagDisableDefaultConfigVars is the flag to force all variables to be set on config-files
	FlagDisableDefaultConfigVars = "disable-default-config-vars"
	// FlagAllowDeprecatedFields is the flag to allow deprecated fields
	FlagAllowDeprecatedFields = "allow-deprecated-fields"

	EnvVarPrefix       = "CDK"
	ConfigType         = "toml"
	SaveConfigFileName = "aggkit_config.toml"

	DefaultCreationFilePermissions = os.FileMode(0600)

	bridgeAddrSetOnWrongSection = "Bridge contract address must be set in the root of " +
		"config file as polygonBridgeAddr."
	specificL2URLDeprecated        = "Use L2URL instead"
	bridgeMetadataAsHashDeprecated = "BridgeMetaDataAsHash is deprecated, " +
		"bridge metadata is always stored as hash."
	aggsenderAgglayerURLDeprecated = "AggSender.AggLayerURL is deprecated, " +
		"use AggSender.AgglayerClient instead"
	aggsenderAggchainProofURLDeprecated = "AggSender.AggchainProofURL is deprecated, " +
		"use AggSender.AggkitProverClient instead"
	aggchainProofGenAggchainProofURLDeprecated = "AggchainProofGen.AggchainProofURL is deprecated, " +
		"use AggSender.AggkitProverClient instead"
	aggsenderUseAgglayerTLSDeprecated = "AggSender.UseAgglayerTLS is deprecated, " +
		"use AggSender.AgglayerClient.UseTLS instead"
	aggsenderUseAggkitProverTLSDeprecated = "AggSender.UseAggkitProverTLS is deprecated, " +
		"use AggSender.AggkitProverClient.UseTLS instead"
	aggsenderAggchainProofTimeoutDeprecated = "AggSender.GenerateAggchainProofTimeout is deprecated, " +
		"use AggSender.AggkitProverClient.RequestTimeout instead"
	aggchainProofGenAggchainProofTimeoutDeprecated = "AggchainProofGen.GenerateAggchainProofTimeout is deprecated, " +
		"use AggchainProofGen.AggkitProverClient.RequestTimeout instead"
)

type DeprecatedFieldsError struct {
	// key is the rule and the value is the field's name that matches the rule
	Fields map[DeprecatedField][]string
}

func NewErrDeprecatedFields() *DeprecatedFieldsError {
	return &DeprecatedFieldsError{
		Fields: make(map[DeprecatedField][]string),
	}
}

func (e *DeprecatedFieldsError) AddDeprecatedField(fieldName string, rule DeprecatedField) {
	p := e.Fields[rule]
	e.Fields[rule] = append(p, fieldName)
}

func (e *DeprecatedFieldsError) Error() string {
	res := "found deprecated fields:"
	for rule, fieldsMatches := range e.Fields {
		res += fmt.Sprintf("\n\t- %s: %s", rule.Reason, strings.Join(fieldsMatches, ", "))
	}
	return res
}

type DeprecatedField struct {
	// If the field name ends with a dot means that match a section
	FieldNamePattern string
	Reason           string
}

var (
	deprecatedFieldsOnConfig = []DeprecatedField{
		{
			FieldNamePattern: "L1Config.polygonBridgeAddr",
			Reason:           bridgeAddrSetOnWrongSection,
		},
		{
			FieldNamePattern: "L2Config.polygonBridgeAddr",
			Reason:           bridgeAddrSetOnWrongSection,
		},
		{
			FieldNamePattern: "AggOracle.EVMSender.URLRPCL2",
			Reason:           specificL2URLDeprecated,
		},
		{
			FieldNamePattern: "AggSender.URLRPCL2",
			Reason:           specificL2URLDeprecated,
		},
		{
			FieldNamePattern: "AggSender.BridgeMetadataAsHash",
			Reason:           bridgeMetadataAsHashDeprecated,
		},
		{
			FieldNamePattern: "AggSender.AggLayerURL",
			Reason:           aggsenderAgglayerURLDeprecated,
		},
		{
			FieldNamePattern: "AggSender.AggchainProofURL",
			Reason:           aggsenderAggchainProofURLDeprecated,
		},
		{
			FieldNamePattern: "AggchainProofGen.AggchainProofURL",
			Reason:           aggchainProofGenAggchainProofURLDeprecated,
		},
		{
			FieldNamePattern: "AggSender.UseAgglayerTLS",
			Reason:           aggsenderUseAgglayerTLSDeprecated,
		},
		{
			FieldNamePattern: "AggSender.UseAggkitProverTLS",
			Reason:           aggsenderUseAggkitProverTLSDeprecated,
		},
		{
			FieldNamePattern: "AggSender.GenerateAggchainProofTimeout",
			Reason:           aggsenderAggchainProofTimeoutDeprecated,
		},
		{
			FieldNamePattern: "AggchainProofGen.GenerateAggchainProofTimeout",
			Reason:           aggchainProofGenAggchainProofTimeoutDeprecated,
		},
	}
)

/*
Config represents the configuration of the entire CDK Node
The file is [TOML format]

[TOML format]: https://en.wikipedia.org/wiki/TOML
*/
type Config struct {
	// Configuration of the etherman (client for access L1)
	Etherman ethermanconfig.Config

	// Configure Log level for all the services, allow also to store the logs in a file
	Log log.Config

	// Configuration of the genesis of the network. This is used to known the initial state of the network
	NetworkConfig NetworkConfig

	// Common Config that affects all the services
	Common common.Config

	// REST contains the configuration settings for the REST service in the Aggkit
	REST common.RESTConfig

	// Configuration of the reorg detector service to be used for the L1
	ReorgDetectorL1 reorgdetector.Config

	// Configuration of the reorg detector service to be used for the L2
	ReorgDetectorL2 reorgdetector.Config

	// Configuration of the aggOracle service
	AggOracle aggoracle.Config

	// Configuration of the L1 Info Treee Sync service
	L1InfoTreeSync l1infotreesync.Config

	// RPC is the config for the RPC server
	RPC jRPC.Config

	// BridgeL1Sync is the configuration for the synchronizer of the bridge of the L1
	BridgeL1Sync bridgesync.Config

	// BridgeL2Sync is the configuration for the synchronizer of the bridge of the L2
	BridgeL2Sync bridgesync.Config

	// LastGERSync is the config for the synchronizer in charge of syncing the last GER injected on L2.
	// Needed for the bridge service (RPC)
	LastGERSync lastgersync.Config

	// AggSender is the configuration of the agg sender service
	AggSender aggsendercfg.Config

	// Prometheus is the configuration of the prometheus service
	Prometheus prometheus.Config

	// AggchainProofGen is the configuration of the Aggchain Proof Generation Tool
	AggchainProofGen prover.Config

	// Profiling is the configuration of the profiling service
	Profiling pprof.Config
}

// Load loads the configuration
func Load(ctx *cli.Context) (*Config, error) {
	configFilePath := ctx.StringSlice(FlagCfg)
	filesData, err := readFiles(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading files:  Err:%w", err)
	}
	saveConfigPath := ctx.String(FlagSaveConfigPath)
	defaultConfigVars := !ctx.Bool(FlagDisableDefaultConfigVars)
	allowDeprecatedFields := ctx.Bool(FlagAllowDeprecatedFields)
	return LoadFile(filesData, saveConfigPath, defaultConfigVars, allowDeprecatedFields)
}

func readFiles(files []string) ([]FileData, error) {
	result := make([]FileData, 0, len(files))
	for _, file := range files {
		fileContent, err := readFileToString(file)
		if err != nil {
			return nil, fmt.Errorf("error reading file content: %s. Err:%w", file, err)
		}
		fileExtension := getFileExtension(file)
		if fileExtension != ConfigType {
			fileContent, err = convertFileToToml(fileContent, fileExtension)
			if err != nil {
				return nil, fmt.Errorf("error converting file: %s from %s to TOML. Err:%w", file, fileExtension, err)
			}
		}
		result = append(result, FileData{Name: file, Content: fileContent})
	}
	return result, nil
}

func getFileExtension(fileName string) string {
	return fileName[strings.LastIndex(fileName, ".")+1:]
}

// Load loads the configuration
func LoadFileFromString(configFileData string, configType string) (*Config, error) {
	cfg := &Config{}
	err := loadString(cfg, configFileData, configType, true, EnvVarPrefix)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func SaveConfigToFile(cfg *Config, saveConfigPath string) error {
	marshaled, err := toml.Marshal(cfg)
	if err != nil {
		log.Errorf("Can't marshal config to toml. Err: %w", err)
		return err
	}
	return SaveDataToFile(saveConfigPath, "final config file", marshaled)
}

func SaveDataToFile(fullPath, reason string, data []byte) error {
	log.Infof("Writing %s to: %s", reason, fullPath)
	err := os.WriteFile(fullPath, data, DefaultCreationFilePermissions)
	if err != nil {
		err = fmt.Errorf("error writing %s to file %s. Err: %w", reason, fullPath, err)
		log.Error(err)
		return err
	}
	return nil
}

// Load loads the configuration
func LoadFile(files []FileData, saveConfigPath string,
	setDefaultVars bool, allowDeprecatedFields bool) (*Config, error) {
	log.Infof("Loading configuration: saveConfigPath: %s, setDefaultVars: %t, allowDeprecatedFields: %t",
		saveConfigPath, setDefaultVars, allowDeprecatedFields)
	fileData := make([]FileData, 0)
	if setDefaultVars {
		log.Info("Setting default vars")
		fileData = append(fileData, FileData{Name: "default_mandatory_vars", Content: DefaultMandatoryVars})
	}
	fileData = append(fileData, FileData{Name: "default_vars", Content: DefaultVars})
	fileData = append(fileData, FileData{Name: "default_values", Content: DefaultValues})
	fileData = append(fileData, files...)

	merger := NewConfigRender(fileData, EnvVarPrefix)

	renderedCfg, err := merger.Render()
	if err != nil {
		return nil, err
	}
	if saveConfigPath != "" {
		fullPath := filepath.Join(saveConfigPath, fmt.Sprintf("%s.merged", SaveConfigFileName))
		err = SaveDataToFile(fullPath, "merged config file", []byte(renderedCfg))
		if err != nil {
			return nil, err
		}
	}
	cfg, err := LoadFileFromString(renderedCfg, ConfigType)
	// If allowDeprecatedFields is true, we ignore the deprecated fields
	if err != nil && allowDeprecatedFields {
		var customErr *DeprecatedFieldsError
		if errors.As(err, &customErr) {
			log.Warnf("detected deprecated fields: %s", err.Error())
			err = nil
		}
	}

	if err != nil {
		return nil, err
	}
	if saveConfigPath != "" {
		fullPath := saveConfigPath + "/" + SaveConfigFileName
		err = SaveConfigToFile(cfg, fullPath)
		if err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// Load loads the configuration
func loadString(cfg *Config, configData string, configType string,
	allowEnvVars bool, envPrefix string) error {
	viper.SetConfigType(configType)
	if allowEnvVars {
		replacer := strings.NewReplacer(".", "_")
		viper.SetEnvKeyReplacer(replacer)
		viper.SetEnvPrefix(envPrefix)
		viper.AutomaticEnv()
	}
	err := viper.ReadConfig(bytes.NewBuffer([]byte(configData)))
	if err != nil {
		return err
	}
	decodeHooks := []viper.DecoderConfigOption{
		// this allows arrays to be decoded from env var separated by ",", example: MY_VAR="value1,value2,value3"
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)),
	}

	err = viper.Unmarshal(&cfg, decodeHooks...)
	if err != nil {
		return err
	}
	configKeys := viper.AllKeys()
	err = checkDeprecatedFields(configKeys)
	if err != nil {
		return err
	}

	return nil
}

func checkDeprecatedFields(keysOnConfig []string) error {
	err := NewErrDeprecatedFields()
	for _, key := range keysOnConfig {
		forbbidenInfo := getDeprecatedField(key)
		if forbbidenInfo != nil {
			err.AddDeprecatedField(key, *forbbidenInfo)
		}
	}
	if len(err.Fields) > 0 {
		return err
	}
	return nil
}

func getDeprecatedField(fieldName string) *DeprecatedField {
	for _, deprecatedField := range deprecatedFieldsOnConfig {
		if strings.ToLower(deprecatedField.FieldNamePattern) == strings.ToLower(fieldName) {
			return &deprecatedField
		}
		// If the field name ends with a dot, it means FieldNamePattern*
		if deprecatedField.FieldNamePattern[len(deprecatedField.FieldNamePattern)-1] == '.' &&
			strings.HasPrefix(fieldName, deprecatedField.FieldNamePattern) {
			return &deprecatedField
		}
	}
	return nil
}
