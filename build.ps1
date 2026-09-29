# MagDownloader 客户端编译脚本
# 用法：powershell -ExecutionPolicy Bypass -File build.ps1 [-Aria2 <aria2c.exe 路径>] [-Target exe]
# 会自动定位 aria2c.exe 并以 /resource 形式内嵌，避免漏加资源导致「程序内未找到内嵌引擎」。
param(
    [string]$Aria2 = "",
    [string]$Target = "exe"
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$root = $PSScriptRoot
$csc  = "$env:WINDIR\Microsoft.NET\Framework64\v4.0.30319\csc.exe"
if (-not (Test-Path $csc)) { throw "找不到 csc.exe：$csc" }

# 源码清单：与 Program.cs 同目录，按依赖顺序即可，csc 不要求顺序
$srcs = @(
    "Program.cs", "MainForm.cs", "AppCore.cs", "TorrentSearch.cs", "HistoryStore.cs",
    "CloudClient.cs", "LoginForm.cs", "LoginMemory.cs", "CaptchaPass.cs", "AccountForms.cs", "FilePreviewForm.cs",
    "Captcha.cs", "Assets.cs", "MovieMeta.cs", "MovieBrowse.cs"
)
foreach ($s in $srcs) {
    if (-not (Test-Path (Join-Path $root $s))) { throw "缺少源文件：$s" }
}

# 定位 aria2c.exe：显式参数 > 本地已解包副本 > 常见工具目录 > PATH
function Find-Aria2 {
    param([string]$explicit)
    $cands = @()
    if ($explicit) { $cands += $explicit }
    # 环境变量优先，换机器时不必改脚本
    if ($env:ARIA2_PATH) { $cands += $env:ARIA2_PATH }
    $cands += (Join-Path $env:LOCALAPPDATA "MagDownloader\aria2c.exe")
    $cands += (Join-Path $root "aria2c.exe")
    # 工具目录：ARIA2_DIR 优先，其次项目同级的 .torrent-tools（兼容原有布局）
    $toolRoots = @()
    if ($env:ARIA2_DIR) { $toolRoots += $env:ARIA2_DIR }
    $toolRoots += (Join-Path (Split-Path $root -Parent) ".torrent-tools\aria2")
    foreach ($tr in $toolRoots) {
        if (-not (Test-Path $tr)) { continue }
        Get-ChildItem $tr -Filter "aria2c.exe" -Recurse -ErrorAction SilentlyContinue |
            ForEach-Object { $cands += $_.FullName }
    }
    $cmd = Get-Command aria2c.exe -ErrorAction SilentlyContinue
    if ($cmd) { $cands += $cmd.Source }
    foreach ($c in $cands) {
        if ($c -and (Test-Path $c)) { return (Resolve-Path $c).Path }
    }
    return $null
}

$aria = Find-Aria2 $Aria2
if (-not $aria) {
    throw "找不到 aria2c.exe，无法内嵌下载引擎。请用 -Aria2 <路径> 指定，或先运行一次旧版客户端让它解包到 %LOCALAPPDATA%\MagDownloader\aria2c.exe"
}
Write-Host ("下载引擎 : {0}  ({1:N0} 字节)" -f $aria, (Get-Item $aria).Length)

$out = Join-Path $root "MagDownloader.exe"
$cscArgs = @(
    "/nologo", "/target:winexe", "/out:$out",
    # 显式声明源码为 UTF-8：csc 默认按系统 ANSI(936) 读，会把中文字面量解成乱码且不报错
    "/codepage:65001",
    "/resource:horizon.png",
    "/resource:$aria,aria2c.exe",
    "/r:System.dll", "/r:System.Drawing.dll", "/r:System.Windows.Forms.dll",
    "/r:System.Web.Extensions.dll", "/r:System.Core.dll", "/r:System.Security.dll"
) + $srcs

Write-Host "编译中 ..."
& $csc @cscArgs
if ($LASTEXITCODE -ne 0) { throw "csc 编译失败，退出码 $LASTEXITCODE" }

# 校验：确认两个资源都在，避免再出现「程序内未找到内嵌引擎」
$asm = [System.Reflection.Assembly]::LoadFile($out)
$names = $asm.GetManifestResourceNames()
Write-Host "内嵌资源 : $($names -join ', ')"
foreach ($want in @("aria2c.exe", "horizon.png")) {
    if ($names -notcontains $want) { throw "编译结果缺少内嵌资源 $want" }
    $len = $asm.GetManifestResourceStream($want).Length
    Write-Host ("  {0,-12} {1,10:N0} 字节" -f $want, $len)
}
Write-Host ("完成 : {0}  ({1:N2} MB)" -f $out, ((Get-Item $out).Length / 1MB))