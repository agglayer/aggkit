//! Command line interface.
use aggkit_config::Config;
use clap::Parser;
use cli::Cli;
use colored::*;
use execute::Execute;
use std::path::PathBuf;
use std::process::Command;

pub mod allocs_render;
mod cli;
mod helpers;
mod logging;
mod versions;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    dotenvy::dotenv().ok();

    let cli = Cli::parse();

    println!(
        "{}",
        r#"🐼
  _____      _                            _____ _____  _  __
 |  __ \    | |                          / ____|  __ \| |/ /
 | |__) |__ | |_   _  __ _  ___  _ __   | |    | |  | | ' / 
 |  ___/ _ \| | | | |/ _` |/ _ \| '_ \  | |    | |  | |  <  
 | |  | (_) | | |_| | (_| | (_) | | | | | |____| |__| | . \ 
 |_|   \___/|_|\__, |\__, |\___/|_| |_|  \_____|_____/|_|\_\
                __/ | __/ |                                 
               |___/ |___/                                  
"#
        .purple()
    );

    match cli.cmd {
        cli::Commands::Node { config, components } => node(config, components)?,
        cli::Commands::Versions {} => versions::versions(),
    }

    Ok(())
}

// read_config reads the configuration file and returns the configuration.
fn read_config(config_path: PathBuf) -> anyhow::Result<Config> {
    let config = std::fs::read_to_string(config_path)
        .map_err(|e| anyhow::anyhow!("Failed to read configuration file: {}", e))?;
    let config: Config = toml::from_str(&config)?;

    Ok(config)
}

/// This is the main node entrypoint.
///
/// This function starts everything needed to run an Agglayer node.
/// Starting by a Tokio runtime which can be used by the different components.
/// The configuration file is parsed and used to configure the node.
///
/// This function returns on fatal error or after graceful shutdown has
/// completed.
pub fn node(config_path: PathBuf, components: Option<String>) -> anyhow::Result<()> {
    // Read the config
    let config = read_config(config_path.clone())?;

    // Initialize the logger
    logging::tracing(&config.log);

    // This is to find the binary when running in development mode
    // otherwise it will use system path
    let bin_path = helpers::get_bin_path();

    let components_param = match components {
        Some(components) => format!("-components={}", components),
        None => "".to_string(),
    };

    // Run the node passing the config file path as argument
    let mut command = Command::new(bin_path.clone());
    command.args(&[
        "run",
        "-cfg",
        config_path.canonicalize()?.to_str().unwrap(),
        components_param.as_str(),
    ]);

    let output_result = command.execute_output();
    let output = match output_result {
        Ok(output) => output,
        Err(e) => {
            eprintln!(
                "Failed to execute command, trying to find executable in path: {}",
                bin_path
            );
            return Err(e.into());
        }
    };

    if let Some(exit_code) = output.status.code() {
        if exit_code == 0 {
            println!("Ok.");
        } else {
            eprintln!("Failed.");
        }
    } else {
        eprintln!("Interrupted!");
    }

    Ok(())
}
