# Common configuration 


## SignerConfig
The `SignerConfig` struct is the primary configuration object used to initialize a signer. It's defined in the [go_signer](https://github.com/agglayer/go_signer) library and specifies how and where cryptographic signing operations are performed.

The configuration supports multiple signer types. To use it, set the desired signer type in the `Method` field. The remaining configuration parameters will vary depending on the selected method.

The main methods are: 

### Keystore (local)
Use this method to sign with a local keystore file.

| Name      | Type   | Example                          | Description                    |
|-----------|--------|----------------------------------|--------------------------------|
| Method    | string | `local`                          | Must be `local`                |
| Path      | string | `/opt/private_key.kestore`       | full path to the keystore      |
| Password  | string | `xdP6G8gV9PYs`                  | password to unlock the keystore |

Example: 
```
[AggSender]
AggsenderPrivateKey = { Method="local", Path="/opt/private_key.kestore", Password="xdP6G8gV9PYs" }
```

### Google Cloud KMS (GCP)
Use this method to sign using the Google Cloud KMS infrastructure.

| Name      | Type   | Example                                                                                    | Description                    |
|-----------|--------|--------------------------------------------------------------------------------------------|--------------------------------|
| Method    | string | `GCP`                                                                                      | Must be `GCP`                  |
| KeyName   | string | projects/your-prj-name/locations/your_location/keyRings/name_of_your_keyring/cryptoKeys/key-name/cryptoKeyVersions/version | id of the key in Google Cloud  |

Example: 
```
[AggSender]
AggsenderPrivateKey = { Method="GCP", KeyName="projects/your-prj-name/locations/your_location/keyRings/name_of_your_keyring/cryptoKeys/key-name/cryptoKeyVersions/version"}
```

### Amazon Web Services KMS (AWS)
Use this method to sign using the AWS KMS infrastructure. The key type must be `ECC_SECG_P256K1` to ensure compatibility.

| Name      | Type   | Example                          | Description                    |
|-----------|--------|----------------------------------|--------------------------------|
| Method    | string | `AWS`                           | Must be `AWS`                  |
| KeyName   | string | `a47c263b-6575-4835-8721-af0bbb97XXXX` | id of the key in AWS           |

Example: 
```
[AggSender]
AggsenderPrivateKey = { Method="AWS", KeyName="a47c263b-6575-4835-8721-af0bbb97XXXX"}
```
## Others
Additional signing methods are available.
For a complete list and detailed configuration options, please refer to the [go_signer library documentation (v0.0.7)](https://github.com/agglayer/go_signer/blob/v0.0.7/README.md)  

## ClientConfig

The `ClientConfig` structure configures the gRPC client connection. It includes the following fields:

| Field Name         | Type           | Description                                                                                |
|--------------------|----------------|--------------------------------------------------------------------------------------------|
| URL                | string         | The URL of the gRPC server                                                                 |
| MinConnectTimeout  | types.Duration | Minimum time to wait for a connection to be established                                    |
| RequestTimeout     | types.Duration | Timeout for individual requests                                                            |
| UseTLS             | bool           | Whether to use TLS for the gRPC connection                                                 |
| Retry              | *RetryConfig   | Retry configuration for failed requests                                                    |

### RetryConfig

The `RetryConfig` structure configures the retry behavior for failed gRPC requests:

| Field Name         | Type           | Description                                                                                |
|--------------------|----------------|--------------------------------------------------------------------------------------------|
| InitialBackoff     | types.Duration | Initial delay before retrying a request                                                    |
| MaxBackoff         | types.Duration | Maximum backoff duration for retries                                                       |
| BackoffMultiplier  | float64        | Multiplier for the backoff duration                                                        |
| MaxAttempts        | int            | Maximum number of retries for a request                                                    |
| Excluded           | []Method       | List of methods excluded from retry policies                                               |

### Method

The `Method` structure identifies a gRPC service and method:

| Field Name    | Type   | Description                                                                                |
|---------------|--------|--------------------------------------------------------------------------------------------|
| ServiceName   | string | The gRPC service name (including package)                                                  |
| MethodName    | string | The gRPC function name (optional)                                                          |

Example:
```
[AggSender]
AgglayerClient = { URL="localhost:50051", MinConnectTimeout="5s", RequestTimeout="5s", UseTLS=false, Retry={ InitialBackoff="100ms", MaxBackoff="10s", BackoffMultiplier=2.0, MaxAttempts=3 } }
```  

## RateLimitConfig

The `RateLimitConfig` structure configures rate limiting behavior. If either `NumRequests` or `Interval` is set to 0, rate limiting is disabled.

| Field Name    | Type           | Description                                                                                |
|---------------|----------------|--------------------------------------------------------------------------------------------|
| NumRequests   | int            | Maximum number of requests allowed within the interval                                     |
| Interval      | types.Duration | Time window for rate limiting                                                              |

Example:
```
[AggSender]
    [AggSender.MaxSubmitCertificateRate]
        NumRequests = 20
        Interval = "1h"
```

When rate limiting is enabled, if the number of requests exceeds `NumRequests` within the specified `Interval`, the system will wait until the next interval before allowing more requests. This helps prevent overwhelming the system with too many requests in a short period.
