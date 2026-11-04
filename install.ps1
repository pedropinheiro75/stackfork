# Install the latest stackfork release binary on Windows.
#
#   irm https://raw.githubusercontent.com/pedropinheiro75/stackfork/main/install.ps1 | iex
#
# $env:STACKFORK_VERSION='v0.2.0'   install that tag instead of the latest release
# $env:STACKFORK_INSTALL_DIR='dir'  install there (default: %LOCALAPPDATA%\Programs\stackfork)
#
# The script block keeps these settings out of the caller's session under iex.
& {
	Set-StrictMode -Version Latest
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is slow with the progress bar on PowerShell 5.1
	$repo = 'pedropinheiro75/stackfork'

	function Say([string]$msg) { Write-Host "stackfork install: $msg" }
	function Die([string]$msg) { throw "stackfork install: error: $msg" }
	function Hint([string]$msg) {
		Die "$msg`nInstall from source instead (Go 1.26.10 or later):`n  go install github.com/$repo/cmd/stackfork@latest"
	}

	# PowerShell 5.1 does not enable TLS 1.2 by default.
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	$cpu = $env:PROCESSOR_ARCHITECTURE
	if ($env:PROCESSOR_ARCHITEW6432) { $cpu = $env:PROCESSOR_ARCHITEW6432 } # 32-bit PowerShell on 64-bit Windows
	switch ($cpu) {
		'AMD64' { $arch = 'amd64' }
		'ARM64' { $arch = 'arm64' }
		default { Die "unsupported architecture: $cpu (amd64 and arm64 only)" }
	}

	$tag = $env:STACKFORK_VERSION
	if (-not $tag) {
		$api = "https://api.github.com/repos/$repo/releases/latest"
		try { $latest = Invoke-RestMethod -UseBasicParsing -Uri $api }
		catch { Hint "no stackfork release found at $api (HTTP 404 means none is published yet)" }
		$tag = $latest | Select-Object -ExpandProperty tag_name -ErrorAction SilentlyContinue
		if (-not $tag) { Hint "could not read the latest release tag from $api" }
	}
	if (-not $tag.StartsWith('v')) { $tag = "v$tag" }

	$archive = "stackfork_$($tag.Substring(1))_windows_$arch.zip"
	$base = "https://github.com/$repo/releases/download/$tag"
	$tmp = Join-Path ([IO.Path]::GetTempPath()) ("stackfork-" + [Guid]::NewGuid())
	New-Item -ItemType Directory -Path $tmp | Out-Null
	try {
		Say "downloading $archive ($tag)"
		$zip = Join-Path $tmp $archive
		try { Invoke-WebRequest -UseBasicParsing -Uri "$base/$archive" -OutFile $zip }
		catch { Hint "could not download $base/$archive (is $tag a published release?)" }
		$sums = Join-Path $tmp 'checksums.txt'
		try { Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums }
		catch { Die "could not download $base/checksums.txt" }

		$want = $null
		foreach ($line in Get-Content $sums) {
			$f = $line -split '\s+'
			if ($f.Count -ge 2 -and $f[1] -eq $archive) { $want = $f[0].ToLowerInvariant() }
		}
		if (-not $want) { Die "$archive is not listed in checksums.txt" }
		$got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
		if ($want -ne $got) { Die "checksum mismatch for $archive (want $want, got $got)" }

		$unzipped = Join-Path $tmp 'x'
		Expand-Archive -Path $zip -DestinationPath $unzipped
		$exe = Join-Path $unzipped 'stackfork.exe'
		if (-not (Test-Path $exe)) { Die "stackfork.exe not found in $archive" }

		$dir = $env:STACKFORK_INSTALL_DIR
		if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\stackfork' }
		New-Item -ItemType Directory -Force -Path $dir | Out-Null
		$dest = Join-Path $dir 'stackfork.exe'
		try { Copy-Item -Force -Path $exe -Destination $dest }
		catch { Die "could not write $dest (is stackfork running? stop it and retry)" }
	}
	finally {
		Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
	}

	Say "installed $dest"
	& $dest version

	$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
	if (-not $userPath) { $userPath = '' }
	$parts = $userPath -split ';' | ForEach-Object { $_.TrimEnd('\') }
	if ($parts -notcontains $dir.TrimEnd('\')) {
		[Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';'), $dir) -join ';').TrimStart(';'), 'User')
		$env:Path = "$env:Path;$dir"
		Say "added $dir to your user PATH; open a new terminal to use it there"
	}
}
