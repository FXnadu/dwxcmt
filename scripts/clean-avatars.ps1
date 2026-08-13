# clean-avatars.ps1 - Remove legacy orphaned avatar cache files under data/avatars (Windows/local).
#
# Background: since v3.3 the avatar cache key changed from "comment ID" to "email md5".
#   Legacy format (orphans): {commentID}.jpg/png/gif/webp and {commentID}.none
#   New format (kept):       {emailMD5}.jpg/png/gif/webp and {emailMD5}.none (32 lowercase hex)
# Removing legacy files is safe: the next request for that comment's avatar will
# re-fetch and cache it again with the new logic.
#
# Usage:
#   .\clean-avatars.ps1 [-DryRun] [-Path data\avatars]
param(
  [switch]$DryRun,
  [string]$Path = "data\avatars"
)

if (-not (Test-Path $Path)) {
  Write-Host "Directory not found: $Path (nothing to clean)"
  exit 0
}

$removed = 0
foreach ($f in Get-ChildItem -File -Path $Path) {
  $base = [System.IO.Path]::GetFileNameWithoutExtension($f.Name)
  # New format: 32 lowercase hex chars (email md5) -> keep
  if ($base -match '^[0-9a-f]{32}$') { continue }
  # Legacy format: pure digits (comment ID) -> orphan, remove
  if ($base -match '^\d+$') {
    if ($DryRun) {
      Write-Host "[dry-run] would remove: $($f.FullName)"
    } else {
      Remove-Item -LiteralPath $f.FullName -Force
      Write-Host "Removed: $($f.FullName)"
    }
    $removed++
  } else {
    Write-Host "Skipped (unrecognized, kept conservatively): $($f.FullName)"
  }
}

if ($DryRun) {
  Write-Host "Dry-run done: $removed legacy file(s) would be removed"
} else {
  Write-Host "Cleanup done: removed $removed legacy file(s)"
}
