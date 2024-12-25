use std::env;

const AGGKIT_CLIENT_BIN: &str = "aggkit";

pub(crate) fn get_bin_path() -> String {
    // This is to find the binary when running in development mode
    // otherwise it will use system path
    let mut bin_path = env::var("CARGO_MANIFEST_DIR").unwrap_or(AGGKIT_CLIENT_BIN.into());
    if bin_path != AGGKIT_CLIENT_BIN {
        bin_path = format!("{}/../../target/{}", bin_path, AGGKIT_CLIENT_BIN);
    }
    bin_path
}
