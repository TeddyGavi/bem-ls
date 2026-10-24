//! Zed extension for bem-ls.
//!
//! The server binary is, in order: `lsp.bem-ls.binary.path` from Zed's
//! settings, `bem-ls` on the PATH, or the matching asset of the latest
//! GitHub release, downloaded into the extension's work directory.
//!
//! Zed has no generic LSP inline completion yet, so ghost text is not
//! shown; the "Insert missing BEM selectors" code action does the same.

use std::fs;

use zed_extension_api::{self as zed, settings::LspSettings, LanguageServerId, Result};

/// GitHub repository the releases come from.
const REPO: &str = "TeddyGavi/bem-ls";
/// Language server name, as in extension.toml.
const SERVER: &str = "bem-ls";

struct BemExtension {
    cached_binary: Option<String>,
}

fn is_file(path: &str) -> bool {
    fs::metadata(path).map(|m| m.is_file()).unwrap_or(false)
}

/// The release asset for a platform, following the goreleaser naming:
/// `bem-ls_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows).
fn asset_name(version: &str, os: zed::Os, arch: zed::Architecture) -> Result<(String, zed::DownloadedFileType)> {
    let (os_name, ext, file_type) = match os {
        zed::Os::Mac => ("darwin", "tar.gz", zed::DownloadedFileType::GzipTar),
        zed::Os::Linux => ("linux", "tar.gz", zed::DownloadedFileType::GzipTar),
        zed::Os::Windows => ("windows", "zip", zed::DownloadedFileType::Zip),
    };
    let arch_name = match arch {
        zed::Architecture::Aarch64 => "arm64",
        zed::Architecture::X8664 => "amd64",
        zed::Architecture::X86 => return Err("bem-ls has no 32-bit x86 build".to_string()),
    };
    let version = version.trim_start_matches('v');
    Ok((format!("{SERVER}_{version}_{os_name}_{arch_name}.{ext}"), file_type))
}

impl BemExtension {
    fn server_binary(&mut self, id: &LanguageServerId, worktree: &zed::Worktree) -> Result<String> {
        if let Some(path) = LspSettings::for_worktree(SERVER, worktree)
            .ok()
            .and_then(|s| s.binary)
            .and_then(|b| b.path)
        {
            return Ok(path);
        }
        if let Some(path) = worktree.which(SERVER) {
            return Ok(path);
        }
        if let Some(path) = &self.cached_binary {
            if is_file(path) {
                return Ok(path.clone());
            }
        }

        zed::set_language_server_installation_status(id, &zed::LanguageServerInstallationStatus::CheckingForUpdate);
        let release = zed::latest_github_release(
            REPO,
            zed::GithubReleaseOptions { require_assets: true, pre_release: false },
        )?;
        let (os, arch) = zed::current_platform();
        let (name, file_type) = asset_name(&release.version, os, arch)?;
        let asset = release
            .assets
            .iter()
            .find(|a| a.name == name)
            .ok_or_else(|| format!("release {} has no asset {name}", release.version))?;

        let dir = format!("{SERVER}-{}", release.version);
        let exe = if matches!(os, zed::Os::Windows) { "bem-ls.exe" } else { "bem-ls" };
        let binary = format!("{dir}/{exe}");
        if !is_file(&binary) {
            zed::set_language_server_installation_status(id, &zed::LanguageServerInstallationStatus::Downloading);
            zed::download_file(&asset.download_url, &dir, file_type)
                .map_err(|e| format!("downloading {name}: {e}"))?;
            zed::make_file_executable(&binary)?;
            // Remove older versions.
            if let Ok(entries) = fs::read_dir(".") {
                for entry in entries.flatten() {
                    let file_name = entry.file_name();
                    let old = file_name.to_string_lossy();
                    if old.starts_with(&format!("{SERVER}-")) && old != dir.as_str() {
                        let _ = fs::remove_dir_all(entry.path());
                    }
                }
            }
        }
        zed::set_language_server_installation_status(id, &zed::LanguageServerInstallationStatus::None);
        self.cached_binary = Some(binary.clone());
        Ok(binary)
    }
}

impl zed::Extension for BemExtension {
    fn new() -> Self {
        Self { cached_binary: None }
    }

    fn language_server_command(&mut self, id: &LanguageServerId, worktree: &zed::Worktree) -> Result<zed::Command> {
        let binary_settings = LspSettings::for_worktree(SERVER, worktree).ok().and_then(|s| s.binary);
        let args = binary_settings
            .as_ref()
            .and_then(|b| b.arguments.clone())
            .unwrap_or_else(|| vec!["--stdio".to_string()]);
        let mut env = worktree.shell_env();
        if let Some(extra) = binary_settings.and_then(|b| b.env) {
            env.extend(extra);
        }
        Ok(zed::Command { command: self.server_binary(id, worktree)?, args, env })
    }

    fn language_server_initialization_options(
        &mut self,
        _id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        Ok(LspSettings::for_worktree(SERVER, worktree).ok().and_then(|s| s.initialization_options))
    }

    fn language_server_workspace_configuration(
        &mut self,
        _id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<Option<zed::serde_json::Value>> {
        // The server asks for the "bem" section.
        let settings = LspSettings::for_worktree(SERVER, worktree).ok().and_then(|s| s.settings);
        Ok(settings.map(|s| zed::serde_json::json!({ "bem": s })))
    }
}

zed::register_extension!(BemExtension);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn asset_names_match_the_release() {
        assert_eq!(
            asset_name("v1.2.3", zed::Os::Mac, zed::Architecture::Aarch64).unwrap().0,
            "bem-ls_1.2.3_darwin_arm64.tar.gz"
        );
        assert_eq!(
            asset_name("1.2.3", zed::Os::Windows, zed::Architecture::X8664).unwrap().0,
            "bem-ls_1.2.3_windows_amd64.zip"
        );
        assert!(asset_name("1.2.3", zed::Os::Linux, zed::Architecture::X86).is_err());
    }
}
