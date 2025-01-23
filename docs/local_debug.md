# Setup environment to local debug on VSCode

## Requirements

* Working and running [kurtosis-cdk](https://github.com/0xPolygon/kurtosis-cdk) environment setup.
* In `test/scripts/env.sh` setup `KURTOSIS_FOLDER` pointing to your setup.

> [!TIP]
> Use your WIP branch in Kurtosis CDK as needed

## 1. Create configuration for this kurtosis environment

```bash
scripts/local_config
```

## 2. Stop the aggkit node started by Kurtosis CDK

```bash
kurtosis service stop aggkit cdk-node-001
```

## 3. Add to vscode launch.json

After execution of `scripts/local_config`, it suggests an entry for `launch.json` configurations
