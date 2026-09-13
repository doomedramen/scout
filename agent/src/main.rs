use anyhow::Result;
use scout_agent::runtime::{run, AgentConfig};

#[tokio::main]
async fn main() -> Result<()> {
    let config = AgentConfig::from_environment_and_args(std::env::args())?;
    run(config).await
}
