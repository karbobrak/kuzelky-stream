<#
Sestaví distribuční zip:  dist\KuzelkyStream-<verze>.zip
  KuzelkyStream\KuzelkyStream.exe   aplikace (bez okna konzole)
  KuzelkyStream\ffmpeg\ffmpeg.exe   FFmpeg + licence a odkaz na zdrojové kódy
  KuzelkyStream\NAVOD.txt           návod pro kuželnu
config.json, data\ a logs\ si aplikace vytvoří sama při prvním spuštění.

Použití:
  .\build.ps1                                  # verze = dnešní datum, FFmpeg z PATH
  .\build.ps1 -Version 1.0.0 -FFmpeg C:\ffmpeg-essentials\bin\ffmpeg.exe
  .\build.ps1 -Version 1.5.0 -Publish -Notes "Co je nového"
      # navíc vydá verzi na GitHubu (Releases) – aplikace v kuželnách se při startu samy aktualizují.
      # Potřebuje GitHub CLI (gh) přihlášené k účtu karbobrak: gh auth login
Doporučený FFmpeg: build "essentials" z https://www.gyan.dev/ffmpeg/builds/ (menší než "full",
obsahuje NVENC, Quick Sync i AMF).
#>
param(
    [string]$Version = (Get-Date -Format "yyyy.M.d"),
    [string]$FFmpeg = $env:KUZELKY_FFMPEG,
    # Google OAuth klient (typ Desktop app) vložený do exe, aby oddíl nemusel nic nastavovat v Google
    # Cloud. Výchozí: z config.json vedle skriptu (sekce youtube). Do gitu se nedostane.
    [string]$ClientId = $env:KUZELKY_CLIENT_ID,
    [string]$ClientSecret = $env:KUZELKY_CLIENT_SECRET,
    # Vydat na GitHub Releases (tag v<verze>): exe, NAVOD.txt, SHA256SUMS a KuzelkyStream.zip.
    [switch]$Publish,
    [string]$Notes = "",
    [string]$Gh = $(if ($env:KUZELKY_GH) { $env:KUZELKY_GH } else { "gh" })
)
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not $ClientId -and (Test-Path config.json)) {
    $yt = (Get-Content config.json -Raw -Encoding utf8 | ConvertFrom-Json).youtube
    $ClientId, $ClientSecret = $yt.clientId, $yt.clientSecret
}
if ($ClientId -and -not $ClientSecret) { throw "Zadán ClientId bez ClientSecret." }
if ($Publish) {
    if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "Pro vydání zadejte verzi ve tvaru 1.5.0 (-Version)." }
    if (-not $ClientId) { throw "Vydání musí mít vestavěný Google OAuth klient." }
    & $Gh auth status *> $null
    if ($LASTEXITCODE) { throw "GitHub CLI není přihlášené – spusťte: $Gh auth login" }
    if (git status --porcelain) { throw "V pracovní kopii jsou necommitnuté změny – vydání musí odpovídat commitu." }
}

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
$ld = "-H=windowsgui -s -w -X main.version=$Version"
if ($ClientId) {
    $ld += " -X main.oauthClientID=$ClientId -X main.oauthClientSecret=$ClientSecret"
    Write-Host "   vestavěný Google OAuth klient: $($ClientId.Split('-')[0])-…"
} else {
    Write-Host "   BEZ vestavěného Google OAuth klienta (Client ID se pak zadává v Nastavení)"
}
# Application icon: a Windows resource (.syso) that go build links into the exe.
go run ./cmd/mkicon -o cmd/kuzelkystream/rsrc_windows_amd64.syso -png internal/web/static/ikona.png
Copy-Item internal/web/static/ikona.png docs/ikona.png -Force
if ($LASTEXITCODE) { throw "ikona se nepodařila vytvořit" }
go build -trimpath -ldflags $ld -o "$out\KuzelkyStream.exe" ./cmd/kuzelkystream
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

if ($Publish) {
    # Assets with fixed names: the app downloads KuzelkyStream.exe + SHA256SUMS, and
    # https://github.com/karbobrak/kuzelky-stream/releases/latest/download/KuzelkyStream.zip is the
    # permanent download link for a new notebook.
    $rel = Join-Path $PSScriptRoot "dist\release-$Version"
    if (Test-Path $rel) { Remove-Item $rel -Recurse -Force }
    New-Item -ItemType Directory $rel | Out-Null
    Copy-Item "$out\KuzelkyStream.exe", "$out\NAVOD.txt" $rel
    Copy-Item $zip "$rel\KuzelkyStream.zip"
    $lines = foreach ($f in "KuzelkyStream.exe", "NAVOD.txt", "KuzelkyStream.zip") {
        "{0}  {1}" -f (Get-FileHash "$rel\$f" -Algorithm SHA256).Hash.ToLower(), $f
    }
    [IO.File]::WriteAllText("$rel\SHA256SUMS", ($lines -join "`n") + "`n")
    if (-not $Notes) { $Notes = "Verze $Version" }
    Write-Host "== vydávám v$Version na GitHubu"
    git push origin HEAD
    & $Gh release create "v$Version" "$rel\KuzelkyStream.exe" "$rel\NAVOD.txt" "$rel\SHA256SUMS" "$rel\KuzelkyStream.zip" `
        --title "Kuželky Stream $Version" --notes $Notes --target (git rev-parse HEAD)
    if ($LASTEXITCODE) { throw "gh release create selhal" }
    Write-Host "vydáno: https://github.com/karbobrak/kuzelky-stream/releases/tag/v$Version"
}
