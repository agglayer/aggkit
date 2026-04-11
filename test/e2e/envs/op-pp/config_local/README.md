# Configurations for run in local (vscode)

## aggkit-parallel.toml
This configuration differents ports to be able to run at the same time as docker `aggkit-001`

To launch using vscode add next configuration to `.vscode/launch.json`: 
```
 {
    "name": "docker-compose aggsender",
    "type": "go",
    "request": "launch",
    "mode": "auto",
    "program": "cmd/",
    "cwd": "${workspaceFolder}",
    "args":[
        "run",
        "-cfg", "test/e2e/envs/op-pp/config_local/aggkit-parallel.toml",
        "-components", "aggsender",
    ]
},
```