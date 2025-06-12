# Common configuration 


## SignerConfig
The `SignerConfig` struct is the primary configuration object used to initialize a signer. It's defined in the [go_signer](https://github.com/agglayer/go_signer) library and specifies how and where cryptographic signing operations are performed.

The configuration supports multiple signer types. To use it, set the desired signer type in the `Method` field. The remaining configuration parameters will vary depending on the selected method.

The main methods are: 

### Keystore (local)
Use this method to sign with a local keystore file.

| Name | Type | Example | Description |
| -----|------|---------|-------------|
| Method | string | `local` | Must be `local` |
| Path | string | `/opt/private_key.kestore`| full path to the keystore |
| Password | string | `xdP6G8gV9PYs`| password to unlock the keystore |

Example: 
```
[AggSender]
AggsenderPrivateKey = { Method="local", Path="/opt/private_key.kestore", Password="xdP6G8gV9PYs" }
```

### Google Cloud KMS (GCP)
Use this method to sign using the Google Cloud KMS infrastructure.

| Name | Type | Example | Description |
| -----|------|---------|-------------|
| Method | string | `GCP` | Must be `GCP` |
| KeyName | string |  | id of the key in Google Cloud |

Example: 
```
[AggSender]
AggsenderPrivateKey = { Method="GCP", KeyName="projects/your-prj-name/locations/your_location/keyRings/name_of_your_keyring/cryptoKeys/key-name/cryptoKeyVersions/version"}
```

### Amazon Web Services KMS (AWS)
Use this method to sign using the AWS KMS infrastructure. The key type must be `ECC_SECG_P256K1` to ensure compatibility.

| Name | Type | Example | Description |
| -----|------|---------|-------------|
| Method | string | `AWS` | Must be `AWS` |
| KeyName | string | `a47c263b-6575-4835-8721-af0bbb97XXXX` | id of the key in AWS |

Example: 
```
[AggSender]
AggsenderPrivateKey = { Method="AWS", KeyName="a47c263b-6575-4835-8721-af0bbb97XXXX"}
```
## Others
Additional signing methods are available.
For a complete list and detailed configuration options, please refer to the [go_signer library documentation (v0.0.7)](https://github.com/agglayer/go_signer/blob/v0.0.7/README.md)  