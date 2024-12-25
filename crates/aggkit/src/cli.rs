//! Command line interface.
use std::path::PathBuf;

use clap::{Parser, Subcommand, ValueHint};

/// Command line interface.
#[derive(Parser)]
#[command(author, version, about, long_about = None)]
pub(crate) struct Cli {
    #[command(subcommand)]
    pub(crate) cmd: Commands,
}

#[derive(Subcommand)]
pub(crate) enum Commands {
    /// Run the aggkit with the provided configuration
    Node {
        /// The path to the configuration file
        #[arg(
            long,
            short = 'C',
            value_hint = ValueHint::FilePath,
            env = "AGGKIT_CONFIG_PATH"
        )]
        config: PathBuf,

        /// Components to run.
        #[arg(
            long,
            short,
            value_hint = ValueHint::CommandString,
            env = "AGGKIT_COMPONENTS",
        )]
        components: Option<String>,
    },
    /// Run cdk-erigon node with the provided default configuration
    Erigon {
        /// The path to the configuration file
        #[arg(
            long,
            short = 'C',
            value_hint = ValueHint::FilePath,
            env = "AGGKIT_CONFIG_PATH"
        )]
        config: PathBuf,

        /// The path to a chain specification file.
        #[arg(
            long,
            short = 'g',
            value_hint = ValueHint::FilePath,
            env = "AGGKIT_GENESIS_PATH"
        )]
        chain: PathBuf,
    },
    /// Output the corresponding versions of the components
    Versions,
}
