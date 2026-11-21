<#
Sestaví distribuční zip:  dist\KuzelkyStream-<verze>.zip
  KuzelkyStream\KuzelkyStream.exe   aplikace (bez okna konzole)
  KuzelkyStream\ffmpeg\ffmpeg.exe   FFmpeg + licence a odkaz na zdrojové kódy
  KuzelkyStream\NAVOD.txt           návod pro kuželnu
config.json, data\ a logs\ si aplikace vytvoří sama při prvním spuštění.

Použití:
  .\build.ps1                                  # verze = dnešní datum, FFmpeg z PATH
  .\build.ps1 -Version 1.0.0 -FFmpeg C:\ffmpeg-essentials\bin\ffmpeg.exe
Doporučený FFmpeg: build "essentials" z https://www.gyan.dev/ffmpeg/builds/ (menší než "full",
obsahuje NVENC, Quick Sync i AMF).
#>
param(
    [string]$Version = (Get-Date -Format "yyyy.M.d"),
    [string]$FFmpeg = $env:KUZELKY_FFMPEG
)
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# Go: z PATH, nebo přenosná instalace v %USERPROFILE%\sdk.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $sdk = Get-ChildItem "$env:USERPROFILE\sdk\go*\go\bin\go.exe" -ErrorAction SilentlyContinue | Sort-Object FullName | Select-Object -Last 1
    if (-not $sdk) { throw "Go nenalezeno (PATH ani $env:USERPROFILE\sdk)." }
    $env:Path = "$(Split-Path $sdk.FullName);$env:Path"
}

# FFmpeg: parametr, proměnná KUZELKY_FFMPEG, nebo ffmpeg z PATH (odkaz z winget se rozbalí na skutečný soubor).
if (-not $FFmpeg) {
    $cmd = Get-Command ffmpeg -ErrorAction SilentlyContinue
    if (-not $cmd) { throw "FFmpeg nenalezen. Zadejte -FFmpeg <cesta k ffmpeg.exe>." }
    $item = Get-Item $cmd.Source
    $FFmpeg = if ($item.Target) { [string]$item.Target } else { $item.FullName }
}
if (-not (Test-Path $FFmpeg)) { throw "FFmpeg $FFmpeg neexistuje." }

Write-Host "== kontrola a testy"
go vet ./...
if ($LASTEXITCODE) { throw "go vet selhal" }
go test ./...
if ($LASTEXITCODE) { throw "testy selhaly" }

$out = Join-Path $PSScriptRoot "dist\KuzelkyStream"
if (Test-Path $out) { Remove-Item $out -Recurse -Force }
New-Item -ItemType Directory "$out\ffmpeg" -Force | Out-Null

Write-Host "== sestavení verze $Version"
go build -trimpath -ldflags "-H=windowsgui -s -w -X main.version=$Version" -o "$out\KuzelkyStream.exe" ./cmd/kuzelkystream
if ($LASTEXITCODE) { throw "go build selhal" }

Write-Host "== FFmpeg: $FFmpeg"
Copy-Item $FFmpeg "$out\ffmpeg\ffmpeg.exe"
$ffRoot = Split-Path (Split-Path $FFmpeg)
if (Test-Path "$ffRoot\LICENSE") { Copy-Item "$ffRoot\LICENSE" "$out\ffmpeg\LICENSE.txt" }
@"
FFmpeg je samostatný program pod licencí GPL (viz LICENSE.txt), aplikace ho jen spouští.
Původ tohoto sestavení a zdrojové kódy: https://www.gyan.dev/ffmpeg/builds/ a https://ffmpeg.org/download.html
$(& "$out\ffmpeg\ffmpeg.exe" -hide_banner -version | Select-Object -First 1)
"@ | Set-Content "$out\ffmpeg\ZDROJ.txt" -Encoding utf8

Copy-Item docs\NAVOD.txt "$out\NAVOD.txt"

$zip = Join-Path $PSScriptRoot "dist\KuzelkyStream-$Version.zip"
if (Test-Path $zip) { Remove-Item $zip }
Write-Host "== balím $zip"
Compress-Archive -Path $out -DestinationPath $zip -CompressionLevel Optimal

$exe = (Get-Item "$out\KuzelkyStream.exe").Length / 1MB
$ff = (Get-Item "$out\ffmpeg\ffmpeg.exe").Length / 1MB
$z = (Get-Item $zip).Length / 1MB
Write-Host ("hotovo: aplikace {0:N1} MB, ffmpeg {1:N0} MB, zip {2:N0} MB" -f $exe, $ff, $z)
