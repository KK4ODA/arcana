# Checks a packaged Windows build: kiwix-serve.exe starts from the package,
# serves an archive, and loads the Visual C++ runtime from its own folder
# (the copies Arcana ships), not from Windows.
#
# Usage (PowerShell 7): scripts/check-windows-runtime.ps1 <arcana-...-windows-amd64.zip> <archive.zim>
param(
    [Parameter(Mandatory)][string]$Zip,
    [Parameter(Mandatory)][string]$Zim
)
$ErrorActionPreference = 'Stop'

# Unpack inside the current folder: the system temp path can contain 8.3
# short names, which would not compare equal to the loaded modules' paths.
$dir = Join-Path (Get-Location).Path ('wincheck-' + [guid]::NewGuid().ToString('N'))
Expand-Archive -Path $Zip -DestinationPath $dir
$kiwix = Join-Path $dir 'Arcana\bin\windows-amd64\kiwix'
$exe = Join-Path $kiwix 'kiwix-serve.exe'
$zimPath = (Resolve-Path $Zim).Path
$port = 18991
$stderr = Join-Path $dir 'kiwix-stderr.txt'

$p = Start-Process -FilePath $exe -ArgumentList '--address=127.0.0.1', "--port=$port", "`"$zimPath`"" `
    -PassThru -NoNewWindow -RedirectStandardError $stderr
try {
    $ok = $false
    for ($i = 0; $i -lt 60 -and -not $ok; $i++) {
        Start-Sleep -Milliseconds 500
        if ($p.HasExited) {
            throw ('kiwix-serve exited with status 0x{0:X8}: {1}' -f $p.ExitCode, (Get-Content $stderr -Raw))
        }
        try {
            $ok = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:$port/" -TimeoutSec 5).StatusCode -eq 200
        } catch { }
    }
    if (-not $ok) { throw 'kiwix-serve did not answer' }

    $modules = (Get-Process -Id $p.Id).Modules
    foreach ($name in 'msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll') {
        $m = $modules | Where-Object { $_.ModuleName -ieq $name } | Select-Object -First 1
        if (-not $m) { throw "$name is not loaded" }
        '{0,-20} {1,-16} {2}' -f $name, $m.FileVersionInfo.FileVersion, $m.FileName
        $from = Split-Path $m.FileName -Parent
        if ($from -ne $kiwix) { throw "$name was loaded from $from, not from $kiwix" }
    }
    'kiwix-serve runs with the bundled Visual C++ runtime.'
} finally {
    if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force }
}
