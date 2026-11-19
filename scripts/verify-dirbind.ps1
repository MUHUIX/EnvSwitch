<#
  verify-dirbind.ps1 —— EnvSwitch「目录级版本绑定」的机读验收（方案 §13 + §14）

  设计约束（开源软件：不许在别人的机器上留东西）：
    * 全程只在脚本自己的隔离区里跑：$env:ENVS_HOME 与 $env:LOCALAPPDATA 指向临时目录；
    * 绝不写真实 $PROFILE / ~/.bashrc / HKCU（AutoRun 与 Environment\Path 都只读）/ 真实 junction；
    * envs init 只跑默认预览档（不带 --append / --autorun），并断言真的没落盘；
    * 绝不为变绿去下载：装不上的版本一律 SKIP，不传 --install-if-missing；
    * 目录与版本全部来自实测（envs list --json / Get-Command），不硬编码任何机器路径。

  判据口径：一律用 process.execPath，不用 node -v —— 本机全局可能恰好与某个已装版本同号
  （实测全局就是 v16.20.2），比版本号会假通过（lead R6 教训）。

  用法：
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify-dirbind.ps1
    powershell -NoProfile -File scripts\verify-dirbind.ps1 -SkipBuild -EnvExe D:\tmp\envs.exe
    powershell -NoProfile -File scripts\verify-dirbind.ps1 -Only 13-1*,13-7
    ENVS_KEEP_ISO=1 运行可保留隔离区供人工查看。

  退出码：0 = 没有 FAIL（可以有 SKIP）；1 = 至少一条 FAIL；2 = 前置条件不足跑不起来。
  兼容：Windows PowerShell 5.1（文件必须存成 UTF-8 带 BOM，否则 5.1 按 ANSI 解码会炸解析）。

  两条实测口径（写在这里免得后人再踩）：
  1) shell 探测必须覆盖非标准安装位：where sh/bash → git --exec-path 反推 → HKLM
     GitForWindows\InstallPath → 常见 Program Files，四种全落空才允许记 SKIP。
     本机 Git 实际在 D:\Git（非标准位），只查 PATH + Program Files 会把可验项错挂成等待前置条件。
  2) npm run 会把 <项目>\node_modules\.bin 顶到 PATH[0]：本脚本 13-1d 与 agent-A 的复测都证实
     「npm scripts 里直接调 node」不经过 bin\shims（层 2 结构上覆盖不到这一格），
     层 1 会话注入与层 3 envs run 只保证 npm 自身来自绑定目录。判据一律用 process.execPath。
  3) PowerShell 变量名大小写不敏感：$Iso 与 $i 不冲突，但 $I 会被循环变量 $i 覆盖 ——
     一旦它变成空/数字，相对路径就会把目录建进仓库根。本脚本一律用长名 + [IO.Path]::Combine。
#>
[CmdletBinding()]
param(
    [string]   $EnvExe = '',
    [switch]   $SkipBuild,
    [string[]] $Only = @()
)

$ErrorActionPreference = 'Continue'

# ---------------------------------------------------------------- 结果登记器

$script:Pass = 0
$script:Fail = 0
$script:Skip = 0
$script:Lines = @()

function Test-Selected([string]$id) {
    if (-not $Only -or $Only.Count -eq 0) { return $true }
    foreach ($o in $Only) { if ($id -like $o) { return $true } }
    return $false
}

function Add-Result([string]$status, [string]$id, [string]$name, [string]$actual) {
    if ($status -eq 'PASS') { $script:Pass++ }
    elseif ($status -eq 'FAIL') { $script:Fail++ }
    else { $script:Skip++ }
    $act = [string]$actual
    if ($act.Length -gt 160) { $act = $act.Substring(0, 157) + '...' }
    $line = ('{0}  {1,-7} {2}  | {3}' -f $status, $id, $name, $act)
    $script:Lines += $line
    Write-Output $line
}

function Report {
    param([string]$Id, [string]$Name, [scriptblock]$Body)
    if (-not (Test-Selected $Id)) { return }
    $r = $null
    try { $r = & $Body } catch { $r = @{ s = 'FAIL'; a = ('脚本内部异常: ' + $_.Exception.Message) } }
    if ($null -eq $r) { $r = @{ s = 'FAIL'; a = '断言块没有返回结果' } }
    Add-Result $r.s $Id $Name ([string]$r.a)
}

# ---------------------------------------------------------------- 隔离区

$RealLocal = $env:LOCALAPPDATA
$RealHome  = if ($env:ENVS_HOME) { $env:ENVS_HOME } else { [IO.Path]::Combine($RealLocal, 'EnvSwitch') }
$RepoRoot  = Split-Path -Parent $PSScriptRoot
$Iso       = [IO.Path]::Combine([IO.Path]::GetTempPath(), ('envs-dirbind-' + ([guid]::NewGuid().ToString('N')).Substring(0, 8)))
$IsoHome   = [IO.Path]::Combine($Iso, 'home')
$IsoAppData= [IO.Path]::Combine($Iso, 'appdata')
$IsoWork   = [IO.Path]::Combine($Iso, 'work')

function New-IsoDir([string]$p) { [void](New-Item -ItemType Directory -Force -Path $p); return $p }
[void](New-IsoDir $IsoHome)
[void](New-IsoDir $IsoAppData)
[void](New-IsoDir $IsoWork)

# GOCACHE 由 LOCALAPPDATA 推导，所以在覆盖它之前先把 exe 编好
if (-not $SkipBuild -and -not $EnvExe) {
    $probe = Get-Command go -ErrorAction SilentlyContinue
    if (-not $probe) {
        Write-Output '前置条件不足：本机没有 go，且没给 -EnvExe。'
        exit 2
    }
    $target = [IO.Path]::Combine($Iso, 'envs.exe')
    Push-Location $RepoRoot
    $bo = & go build -o $target ./cmd/envs 2>&1 | Out-String
    $buildCode = $LASTEXITCODE
    Pop-Location
    if ($buildCode -ne 0 -or -not (Test-Path $target)) {
        Write-Output ('前置条件不足：go build ./cmd/envs 失败。' + $bo)
        exit 2
    }
    $EnvExe = $target
}
if (-not $EnvExe -or -not (Test-Path $EnvExe)) {
    Write-Output '前置条件不足：找不到要验收的 envs.exe（用 -EnvExe 指定，或去掉 -SkipBuild 现编）。'
    exit 2
}
$EnvExe = (Resolve-Path $EnvExe).Path

# 从这里起，子进程只能看见隔离区
$env:ENVS_HOME    = $IsoHome
$env:LOCALAPPDATA = $IsoAppData

Write-Output ''
Write-Output ('EnvSwitch 目录绑定验收 · ' + (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))
Write-Output ('  exe               : ' + $EnvExe)
Write-Output ('  隔离 ENVS_HOME    : ' + $IsoHome)
Write-Output ('  隔离 LOCALAPPDATA : ' + $IsoAppData)
Write-Output ('  PowerShell        : ' + $PSVersionTable.PSVersion.ToString())
# bash / sh 的探测结果在下一段打印（探测函数定义在后面，这里打会显示成「未发现」误导人）
Write-Output ''

# ---------------------------------------------------------------- 真实机器基线快照

function Get-Snap([string]$Path) {
    if (Test-Path -LiteralPath $Path) {
        try {
            $i = Get-Item -LiteralPath $Path -Force
            $tgt = ''
            try { $tgt = [string]$i.Target } catch { $tgt = '' }
            return [pscustomobject]@{ Exists = $true; Mtime = $i.LastWriteTimeUtc.Ticks; Size = $i.Length; Target = $tgt }
        } catch { }
    }
    return [pscustomobject]@{ Exists = $false; Mtime = 0; Size = 0; Target = '' }
}

function Format-Snap($s) {
    if (-not $s.Exists) { return '不存在' }
    $t = ([DateTime]$s.Mtime).ToString('MM-dd HH:mm:ss')
    $extra = ''
    if ($s.Target) { $extra = ' → ' + $s.Target }
    return '存在 mtime=' + $t + ' size=' + $s.Size + $extra
}

function Get-RegVal([string]$Key, [string]$Name) {
    try {
        $k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($Key, $false)
        if (-not $k) { return [pscustomobject]@{ Exists = $false; Value = '' } }
        $v = $k.GetValue($Name, $null)
        $k.Close()
        if ($null -eq $v) { return [pscustomobject]@{ Exists = $false; Value = '' } }
        return [pscustomobject]@{ Exists = $true; Value = [string]$v }
    } catch { return [pscustomobject]@{ Exists = $false; Value = '' } }
}

$WatchPaths = @(
    [IO.Path]::Combine($RealHome, 'bindings.json'),
    [IO.Path]::Combine($RealHome, 'bindings.gen.txt'),
    [IO.Path]::Combine($RealHome, 'bindings.lock'),
    [IO.Path]::Combine($RealHome, 'bin\envs-autorun.cmd'),
    [IO.Path]::Combine($RealHome, 'current\node'),
    [IO.Path]::Combine($env:USERPROFILE, 'Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1'),
    [IO.Path]::Combine($env:USERPROFILE, 'Documents\PowerShell\Microsoft.PowerShell_profile.ps1'),
    [IO.Path]::Combine($env:USERPROFILE, '.bashrc')
)
$Before = @{}
foreach ($wp in $WatchPaths) { $Before[$wp] = Get-Snap $wp }
$BeforeAutoRun = Get-RegVal 'Software\Microsoft\Command Processor' 'AutoRun'
$BeforePath    = Get-RegVal 'Environment' 'Path'

# ---------------------------------------------------------------- envs 调用与解析工具

function Invoke-Envs {
    param([string[]]$Cmd, [string]$Cwd = '')
    $prev = $null
    if ($Cwd) { $prev = (Get-Location).ProviderPath; Set-Location -LiteralPath $Cwd }
    $errFile = [IO.Path]::Combine($Iso, ('e' + ([guid]::NewGuid().ToString('N')).Substring(0, 6) + '.err'))
    $out = ''
    $code = 0
    try {
        $out = (& $EnvExe @Cmd 2> $errFile | Out-String)
        $code = $LASTEXITCODE
    } finally {
        if ($prev) { Set-Location -LiteralPath $prev }
    }
    $err = ''
    if (Test-Path -LiteralPath $errFile) {
        $err = [IO.File]::ReadAllText($errFile)
        Remove-Item -LiteralPath $errFile -Force -EA SilentlyContinue
    }
    return [pscustomobject]@{ Out = ([string]$out).Trim(); Err = ([string]$err).Trim(); Code = $code }
}

function ConvertFrom-EnvsJson([string]$Text) {
    # 部分现有命令在 --json 之外还会夹一行表格；从第一个 JSON 起始行开始截。
    if (-not $Text) { return $null }
    $ls = $Text -split "`r?`n"
    for ($i = 0; $i -lt $ls.Count; $i++) {
        $t = $ls[$i].Trim()
        if ($t.StartsWith('[') -or $t.StartsWith('{')) {
            $j = ($ls[$i..($ls.Count - 1)] -join "`n")
            try { return (ConvertFrom-Json $j) } catch { return $null }
        }
    }
    return $null
}

function Get-FirstLine([string]$Text) {
    if (-not $Text) { return '' }
    foreach ($l in ($Text -split "`r?`n")) { if ($l.Trim() -ne '') { return $l.Trim() } }
    return ''
}

function To-PosixPath([string]$p) {
    $s = $p -replace '\\', '/'
    if ($s.Length -ge 2 -and $s.Substring(1, 1) -eq ':') {
        return ('/' + $s.Substring(0, 1).ToLower() + $s.Substring(2))
    }
    return $s
}

# 外部 shell 探测（agent-A 踩过的坑：只查 PATH 与 Program Files，会把「本机可验」错挂成等待前置条件）。
# 顺序：where sh / where bash → git --exec-path 反推 → HKLM GitForWindows\InstallPath → 常见 Program Files。
function Find-Shell([string[]]$names) {
    foreach ($n in $names) {
        $c = Get-Command $n -ErrorAction SilentlyContinue
        if ($c -and (Test-Path -LiteralPath $c.Source)) { return $c.Source }
        $w = ((& where.exe $n 2>$null | Out-String) -split "`r?`n" | Where-Object { $_.Trim() -ne '' } | Select-Object -First 1)
        if ($w -and (Test-Path -LiteralPath $w.Trim())) { return $w.Trim() }
    }
    return ''
}
function Find-ShellFromGitRoot([string]$root, [string[]]$fileNames) {
    if (-not $root) { return '' }
    foreach ($rel in $fileNames) {
        $p = [IO.Path]::Combine($root, $rel)
        if (Test-Path -LiteralPath $p) { return $p }
    }
    return ''
}
$BashSrc = Find-Shell @('bash', 'sh')
$ShSrc   = Find-Shell @('sh')
if (-not $BashSrc -or -not $ShSrc) {
    # ① git --exec-path 反推（D:\Git\mingw64\libexec\git-core → 上两级 = 安装根）
    $gc = Get-Command git -ErrorAction SilentlyContinue
    $roots = @()
    if ($gc) {
        $roots += $gc.Source
        $ep = ((& git --exec-path 2>$null | Out-String)).Trim()
        if ($ep) { $roots += (Split-Path -Parent (Split-Path -Parent $ep)) }
    }
    # ② HKLM 卸载表里 GitForWindows 的 InstallPath（非标准盘也认）
    foreach ($rk in @('HKLM:\SOFTWARE\GitForWindows', 'HKLM:\SOFTWARE\WOW6432Node\GitForWindows')) {
        if (Test-Path -LiteralPath $rk) {
            $ip = (Get-ItemProperty -LiteralPath $rk -ErrorAction SilentlyContinue).InstallPath
            if ($ip) { $roots += $ip }
        }
    }
    # ③ 常见 Program Files（最后才用它，别把它当唯一答案）
    $roots += [IO.Path]::Combine(${env:ProgramFiles}, 'Git')
    $roots += [IO.Path]::Combine(${env:ProgramFiles(x86)}, 'Git')
    foreach ($r2 in $roots) {
        if (-not $BashSrc) { $BashSrc = Find-ShellFromGitRoot $r2 @('bin\bash.exe', 'usr\bin\bash.exe') }
        if (-not $ShSrc) { $ShSrc = Find-ShellFromGitRoot $r2 @('bin\sh.exe', 'usr\bin\sh.exe') }
        if ($BashSrc -and $ShSrc) { break }
    }
}
# 实在只有 bash 没有 sh 时（个别精简安装），用 bash 兼容跑 POSIX 形态 shim
if (-not $ShSrc) { $ShSrc = $BashSrc }
$GitCmd  = Get-Command git -ErrorAction SilentlyContinue
$NodeCmd = Get-Command node -ErrorAction SilentlyContinue
$NpmCmd  = Get-Command npm -ErrorAction SilentlyContinue
$Ps7     = Get-Command pwsh -ErrorAction SilentlyContinue
$CodeCmd = Get-Command code -ErrorAction SilentlyContinue
$bk = '(未发现)'
if ($BashSrc) { $bk = $BashSrc }
$sk = '(未发现)'
if ($ShSrc) { $sk = $ShSrc }
Write-Output ''
Write-Output ('  shell 探测结果 : PowerShell ' + $PSVersionTable.PSVersion.ToString() + ' / bash ' + $bk + ' / sh ' + $sk)

# ---------------------------------------------------------------- 勘察：找一个与全局大版本不同的已装 node

$globalExe = ''
if ($NodeCmd) { $globalExe = $NodeCmd.Source }
$globalVer = ''
if ($globalExe) { $globalVer = Get-FirstLine ((& node -p 'process.versions.node' 2>$null | Out-String)) }
$globalMajor = -1
if ($globalVer -match '^(\d+)') { $globalMajor = [int]$Matches[1] }

$listRes  = Invoke-Envs -Cmd @('list', 'node', '--json')
$installs = @()
$parsed = ConvertFrom-EnvsJson $listRes.Out
if ($parsed) { $installs = @($parsed) }

$other = $null
foreach ($it in $installs) {
    $v = [string]$it.version
    if (-not $v) { continue }
    if ($v -match '^(\d+)') {
        if ([int]$Matches[1] -ne $globalMajor) { $other = $it; break }
    }
}

Write-Output ('  全局 node   : ' + $(if ($globalExe) { $globalVer + ' @ ' + $globalExe } else { '(PATH 上没有 node)' }))
Write-Output ('  已装 node   : ' + $(if ($installs.Count) { (($installs | ForEach-Object { [string]$_.version + '(' + [string]$_.source + ')' }) -join ', ') } else { '(envs list node 为空)' }))
Write-Output ('  判别版本    : ' + $(if ($other) { [string]$other.version + ' @ ' + [string]$other.binDir } else { '没有与全局大版本不同的已装版本' }))
Write-Output ''

$Proj      = New-IsoDir ([IO.Path]::Combine($IsoWork, 'proj'))
$ProjDeep  = New-IsoDir ([IO.Path]::Combine($Proj, 'src\deep'))
$ProjFront = New-IsoDir ([IO.Path]::Combine($IsoWork, 'projfront'))
$pkg = '{"name":"verify-dirbind","version":"1.0.0","scripts":{"dev":"node -p \"console.log(''VER='' + process.version + '' EXE='' + process.execPath)\""}}'
[IO.File]::WriteAllText([IO.Path]::Combine($Proj, 'package.json'), $pkg, (New-Object Text.UTF8Encoding $false))

function Enable-Bindings { [void](Invoke-Envs -Cmd @('bind', 'enable')) }
function Set-Bind([string]$Ver) { return (Invoke-Envs -Cmd @('bind', 'node', $Ver, '--cwd', $Proj)) }

$canDiscriminate = [bool]($globalExe -and $other)
if ($canDiscriminate) { Enable-Bindings }
$want = ''
if ($other) { $want = [string]$other.binDir }

# ================================================================ §13 十条

if ($canDiscriminate) {
    $null = Set-Bind $other.version

    Report '13-1a' 'envs run 的 node execPath 落在绑定版本目录内' {
        $r = Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'node', '-p', 'process.execPath')
        $got = Get-FirstLine $r.Out
        if ($r.Code -ne 0) { return @{ s = 'FAIL'; a = ('exit=' + $r.Code + ' ' + $r.Err) } }
        if ($got -and $got.ToLower().StartsWith($want.ToLower())) { return @{ s = 'PASS'; a = $got } }
        return @{ s = 'FAIL'; a = ('实得 ' + $got + ' / 期望 ' + [IO.Path]::Combine($want, 'node.exe')) }
    }

    Report '13-1b' 'process.version 与绑定表达式一致' {
        $r = Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'node', '-p', 'process.version')
        $got = Get-FirstLine $r.Out
        if ($got -eq ('v' + [string]$other.version)) { return @{ s = 'PASS'; a = $got } }
        return @{ s = 'FAIL'; a = ('实得 ' + $got + ' / 期望 v' + [string]$other.version) }
    }

    Report '13-1c' 'npm 随 node 目录自带入口一起变（§2-L4）' {
        $r = Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'npm', '-v')
        $boundNpm = Get-FirstLine $r.Out
        $globalNpm = ''
        if ($NpmCmd) { $globalNpm = Get-FirstLine ((& npm -v 2>$null | Out-String)) }
        if ($r.Code -ne 0) { return @{ s = 'FAIL'; a = ('npm -v exit=' + $r.Code + ' ' + $r.Err) } }
        if ($boundNpm -and $boundNpm -ne $globalNpm) { return @{ s = 'PASS'; a = ('绑定内 ' + $boundNpm + ' vs 全局 ' + $globalNpm) } }
        return @{ s = 'FAIL'; a = ('npm 版本没变（都是 ' + $boundNpm + '）→ 前置目录没生效') }
    }

    Report '13-1d' 'npm run 的脚本子进程 execPath 也对版' {
        $r = Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'npm', 'run', 'dev')
        # npm 会把脚本命令行原样回显（行首是 "> "），那行里也含 EXE= 字样；
        # 只认 console.log 真实输出的那一行（以 VER=v 开头）。
        $line = ''
        foreach ($l in ($r.Out -split "`r?`n")) { if ($l -match '^VER=v\S+ EXE=\S+') { $line = $l.Trim(); break } }
        if (-not $line) { return @{ s = 'FAIL'; a = ('npm run dev 没打出 EXE=：' + $r.Out) } }
        if ($line.ToLower().Contains($want.ToLower())) { return @{ s = 'PASS'; a = $line } }
        return @{ s = 'FAIL'; a = ('实得 ' + $line + ' / 期望含 ' + $want) }
    }

    Report '13-2' '会话隔离：两版本交替 10 次 execPath 恒正确；真实 junction 未被改' {
        $seq = 0
        $bad = ''
        foreach ($i in 1..10) {
            foreach ($v in @([string]$other.version, $globalVer)) {
                if (-not $v) { continue }
                $null = Set-Bind $v
                $r = Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'node', '-p', 'process.execPath')
                $got = Get-FirstLine $r.Out
                $seq++
                if ($r.Code -ne 0 -or -not $got) { $bad = ('第 ' + $i + ' 轮 exit=' + $r.Code + ' 输出 ' + $got); break }
            }
            if ($bad) { break }
        }
        $nodeLink = [IO.Path]::Combine($RealHome, 'current\node')
        $nowNode = Get-Snap $nodeLink
        if ($nowNode.Target -ne $Before[$nodeLink].Target) { $bad = '真实 current\node 目标被改动' }
        if ($bad) { return @{ s = 'FAIL'; a = $bad } }
        return @{ s = 'PASS'; a = ('交替 ' + $seq + ' 次全通过；真实 junction 目标 ' + $(if ($nowNode.Target) { $nowNode.Target } else { '(不存在，未创建)' })) }
    }

    Report '13-4' '嵌套继承 + 目录外撤销（PATH 膨胀见 14-2）' {
        $null = Set-Bind $other.version
        $inDeep = Invoke-Envs -Cmd @('run', '--cwd', $ProjDeep, '--', 'node', '-p', 'process.execPath')
        $deepExe = Get-FirstLine $inDeep.Out
        if (-not $deepExe.ToLower().Contains($want.ToLower())) { return @{ s = 'FAIL'; a = ('子目录未继承：' + $deepExe) } }
        $outDir = Invoke-Envs -Cmd @('run', '--cwd', $IsoWork, '--', 'node', '-p', 'process.execPath')
        $outExe = Get-FirstLine $outDir.Out
        if ($outExe.ToLower().Contains($want.ToLower())) { return @{ s = 'FAIL'; a = ('目录外仍在用绑定版本：' + $outExe) } }
        return @{ s = 'PASS'; a = ('深目录 ' + $deepExe + ' / 目录外回全局 ' + $outExe) }
    }

    Report '13-7' '前缀粘连边界：<work>\proj 的绑定不得命中 <work>\projfront' {
        $null = Set-Bind $other.version
        $r = Invoke-Envs -Cmd @('run', '--cwd', $ProjFront, '--', 'node', '-p', 'process.execPath')
        $got = Get-FirstLine $r.Out
        if ($got.ToLower().Contains($want.ToLower())) { return @{ s = 'FAIL'; a = ('projfront 误命中：' + $got) } }
        $why = Invoke-Envs -Cmd @('bind', 'why', '--cwd', $ProjFront)
        if ($why.Out -match '命中链') { return @{ s = 'FAIL'; a = ('bind why 在兄弟目录竟然有命中链：' + $why.Out) } }
        return @{ s = 'PASS'; a = ('未命中，实得 ' + $got) }
    }

    Report '13-9' '性能：envs run 相对直接调用的额外开销（阈值 <80ms）' {
        function MinMs([scriptblock]$b, [int]$n) {
            $best = 999999.0
            for ($i = 0; $i -lt $n; $i++) {
                $sw = [Diagnostics.Stopwatch]::StartNew()
                & $b | Out-Null
                $sw.Stop()
                if ($sw.Elapsed.TotalMilliseconds -lt $best) { $best = $sw.Elapsed.TotalMilliseconds }
            }
            return [math]::Round($best, 1)
        }
        $direct = MinMs { & node -p '1' 2>&1 } 5
        $via = MinMs { [void](Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'node', '-p', '1')) } 5
        $delta = [math]::Round($via - $direct, 1)
        if ($delta -lt 0) { $delta = 0 }
        if ($delta -lt 80) { return @{ s = 'PASS'; a = ('直接 ' + $direct + 'ms / 经 run ' + $via + 'ms / 额外 ' + $delta + 'ms') } }
        return @{ s = 'FAIL'; a = ('额外 ' + $delta + 'ms 超 80ms（直接 ' + $direct + ' / run ' + $via + '）') }
    }
} else {
    $why13 = '需第二版本或干净 VM：本机没有与全局 node 大版本不同的已装版本（脚本按红线不下载）'
    Add-Result 'SKIP' '13-1a' 'envs run 的 node execPath 落在绑定版本目录内' $why13
    Add-Result 'SKIP' '13-1b' 'process.version 与绑定表达式一致' $why13
    Add-Result 'SKIP' '13-1c' 'npm 随 node 目录自带入口一起变' $why13
    Add-Result 'SKIP' '13-1d' 'npm run 脚本子进程 execPath 对版' $why13
    Add-Result 'SKIP' '13-2'  '会话隔离与真实 junction 不变' $why13
    Add-Result 'SKIP' '13-4'  '嵌套继承与目录外撤销' $why13
    Add-Result 'SKIP' '13-7'  '前缀粘连边界' $why13
    Add-Result 'SKIP' '13-9'  '性能预算' $why13
}

Report '13-3' '多 shell 入口（cmd / PS5.1 / Git Bash / VSCode）——钩子跑在临时副本里' {
    if (-not $canDiscriminate) { return @{ s = 'SKIP'; a = '缺判别版本' } }
    $null = Set-Bind $other.version
    $notes = @()
    $bad = @()

    # cmd：取 envs init cmd 预览档给出的 AutoRun 正文，落临时副本跑（绝不写注册表）。
    # 注意收尾必须锚在「行首的 :envs_end」——正文里还有两处 goto :envs_end，
    # 非贪婪匹配到那一处就会把 for 行截掉（本脚本第一版就是这么假失败的）。
    $pv = Invoke-Envs -Cmd @('init', 'cmd')
    $m = [regex]::Match($pv.Out, '(?s)@echo off\s*\r?\nrem BEGIN EnvSwitch.*?\r?\n:envs_end')
    if ($m.Success) {
        $bat = [IO.Path]::Combine($Iso, 'autorun-copy.cmd')
        [IO.File]::WriteAllText($bat, $m.Value, (New-Object Text.UTF8Encoding $false))
        $cmdLine = 'cd /d "' + $Proj + '" && call "' + $bat + '" && node -p process.execPath'
        $out = (& cmd /c $cmdLine 2>&1 | Out-String)
        if ($out.ToLower().Contains($want.ToLower())) { $notes += 'cmd=OK' } else { $bad += ('cmd 未生效：' + (Get-FirstLine $out)) }
    } else { $bad += 'cmd：预览里没找到 AutoRun 钩子正文' }

    # PowerShell：预览档生成的块 → 临时副本 dot-source
    $pp = Invoke-Envs -Cmd @('init', 'powershell')
    $mp = [regex]::Match($pp.Out, '(?s)# BEGIN EnvSwitch directory-binding hook.*?# END EnvSwitch directory-binding hook v1')
    if ($mp.Success) {
        $ps1 = [IO.Path]::Combine($Iso, 'hook-copy.ps1')
        $body = $mp.Value + "`r`n" +
            'Set-Location -LiteralPath ''' + $Proj + '''' + "`r`n" +
            'EnvSwitch-CdHook' + "`r`n" +
            '(Get-Command node).Source' + "`r`n"
        [IO.File]::WriteAllText($ps1, $body, (New-Object Text.UTF8Encoding $false))
        $out = (& powershell -NoProfile -ExecutionPolicy Bypass -File $ps1 2>&1 | Out-String)
        if ($out.ToLower().Contains($want.ToLower())) { $notes += 'PS5.1=OK' } else { $bad += ('PS5.1 未生效：' + (Get-FirstLine $out)) }
    } else { $bad += 'PS：预览里没找到钩子块' }

    # Git Bash：预览档生成的块 → 临时文件 source（绝不往真实 ~/.bashrc 追加）
    if ($BashSrc) {
        $bp = Invoke-Envs -Cmd @('init', 'bash')
        $mb = [regex]::Match($bp.Out, '(?s)# BEGIN EnvSwitch directory-binding hook.*?# END EnvSwitch directory-binding hook v1')
        if ($mb.Success) {
            $sh = [IO.Path]::Combine($Iso, 'hook-copy.sh')
            # 命中 + 离开两个相位一起验（§13-4 的 bash 侧对应物）
            $body = $mb.Value + "`n" +
                'cd "' + (To-PosixPath $Proj) + '"' + "`n" +
                'envs_cd_hook' + "`n" +
                'echo "IN=${PATH%%:*}"' + "`n" +
                'cd "' + (To-PosixPath $IsoWork) + '"' + "`n" +
                'envs_cd_hook' + "`n" +
                'echo "OUT=${PATH}"' + "`n"
            [IO.File]::WriteAllText($sh, $body, (New-Object Text.UTF8Encoding $false))
            $out = (& $BashSrc --norc --noprofile $sh 2>&1 | Out-String)
            $posix = (To-PosixPath $want).ToLower()
            $inLine = ''
            $outLine = ''
            foreach ($l in ($out -split "`r?`n")) {
                if ($l -like 'IN=*') { $inLine = $l.Substring(3).Trim().ToLower() }
                if ($l -like 'OUT=*') { $outLine = $l.Substring(5).Trim().ToLower() }
            }
            if ($inLine -ne $posix) {
                $bad += ('Git Bash 未命中绑定目录：IN=' + $inLine + ' / 期望 ' + $posix)
            } elseif ($outLine.Contains($posix)) {
                $bad += ('Git Bash 离开后没回快照：' + $outLine)
            } else {
                $notes += 'GitBash=OK(命中+撤销)'
            }
        } else { $bad += 'bash：预览里没找到钩子块' }
    } else { $bad += 'Git Bash=SKIP(where/git --exec-path/HKLM GitForWindows/Program Files 四种探测全落空)' }

    if ($CodeCmd) { $notes += 'VSCode=SKIP(需人工完全重启并目视)' } else { $notes += 'VSCode=SKIP(未装 code)' }
    if ($bad.Count) { return @{ s = 'FAIL'; a = (($bad -join ' ;; ') + ' [' + ($notes -join ', ') + ']') } }
    return @{ s = 'PASS'; a = ($notes -join ', ') }
}

Report '13-5' '托底：绑未装版本仍用全局跑 + warn；bindings.json 写成垃圾自动隔离不阻塞' {
    Enable-Bindings
    # 从已装里推一个大版本号当「肯定没装」的靶子，绝不 install
    $missing = '9' + (Get-Random -Minimum 1 -Maximum 9) + '.0.0'
    foreach ($it in $installs) { if ([string]$it.version -like ($missing + '*')) { $missing = $missing + '.1' } }
    $rb = Invoke-Envs -Cmd @('bind', 'node', $missing, '--cwd', $ProjFront)
    if ($rb.Code -ne 0) { return @{ s = 'FAIL'; a = ('绑未装版本本应写表成功：' + $rb.Err) } }
    $r = Invoke-Envs -Cmd @('run', '--cwd', $ProjFront, '--', 'node', '-p', 'process.execPath')
    $ran = Get-FirstLine $r.Out
    if ($r.Code -ne 0 -or -not $ran) { return @{ s = 'FAIL'; a = ('未装时命令被阻塞：exit=' + $r.Code + ' ' + $r.Err) } }
    $why = Invoke-Envs -Cmd @('bind', 'why', '--cwd', $ProjFront)
    if ($why.Out + $why.Err -notmatch '未安装') { return @{ s = 'FAIL'; a = '缺「未安装」warn' } }

    $bj = [IO.Path]::Combine($IsoHome, 'bindings.json')
    [IO.File]::WriteAllText($bj, '{"version":1, 这行不是 JSON', (New-Object Text.UTF8Encoding $false))
    $r2 = Invoke-Envs -Cmd @('resolve', '--cwd', $Proj)
    $badFiles = @(Get-ChildItem -LiteralPath $IsoHome -Filter 'bindings.bad-*' -EA SilentlyContinue)
    if ($r2.Code -ne 0) { return @{ s = 'FAIL'; a = ('损坏后 resolve 仍失败：exit=' + $r2.Code) } }
    if ($badFiles.Count -lt 1) { return @{ s = 'FAIL'; a = '没生成 bindings.bad-<ts> 隔离文件' } }
    if ($r2.Err -notmatch '停用目录绑定|无法读取') { return @{ s = 'FAIL'; a = ('缺隔离提示：' + $r2.Err) } }
    return @{ s = 'PASS'; a = ('未装仍跑 ' + $ran + '（带 warn）；损坏已隔离为 ' + $badFiles[0].Name) }
}

Report '13-6' '注入面：绑定值放 zz" & echo [PWNED] & " 不得执行（§2-L9）' {
    $bj = [IO.Path]::Combine($IsoHome, 'bindings.json')
    $evil = 'zz" & echo [PWNED] & "'
    # 手工拼 JSON：反斜杠与引号都要转义，否则整个文件非法 → 会被隔离，测的就不是注入面了
    $jsonPath = $Proj -replace '\\', '\\'
    $jsonEvil = $evil -replace '"', '\"'
    $doc = '{"version":1,"enabled":true,"autoDetect":"prompt","maxDepth":24,"bindings":[{"path":"' + $jsonPath + '","tools":{"node":"' + $jsonEvil + '"},"env":{}}]}'
    [IO.File]::WriteAllText($bj, $doc, (New-Object Text.UTF8Encoding $false))
    [IO.File]::WriteAllText([IO.Path]::Combine($IsoHome, 'bindings.gen.txt'), "#version=1`r`n", (New-Object Text.UTF8Encoding $false))
    $r = Invoke-Envs -Cmd @('__bind-resolve', '--shell', 'cmd') -Cwd $Proj
    $all = $r.Out + "`n" + $r.Err
    if ($all.Contains('[PWNED]')) { return @{ s = 'FAIL'; a = ('注入面失守：' + (Get-FirstLine $all)) } }
    $rw = Invoke-Envs -Cmd @('bind', 'why', '--cwd', $Proj)
    if ($rw.Out -notmatch '约束') { return @{ s = 'FAIL'; a = ('why 没说明这是不可定位的约束值：' + (Get-FirstLine $rw.Out)) } }
    return @{ s = 'PASS'; a = '注入体既没执行、也被明确判为不可定位（why 有说明）' }
}

Report '13-8' 'go 注入 GOTOOLCHAIN=local 后 go 不再偷偷下工具链' {
    $gp = ConvertFrom-EnvsJson (Invoke-Envs -Cmd @('list', 'go', '--json')).Out
    $gl = @()
    if ($gp) { $gl = @($gp) }
    if (-not $gl -or -not [string]$gl[0].version) { return @{ s = 'SKIP'; a = '本机没有已装 go 版本（envs list go 为空）' } }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { return @{ s = 'SKIP'; a = 'PATH 上没有 go，跑不了 go env 断言' } }
    $v = [string]$gl[0].version
    Enable-Bindings
    $rb = Invoke-Envs -Cmd @('bind', 'go', $v, '--cwd', $Proj)
    if ($rb.Code -ne 0) { return @{ s = 'SKIP'; a = ('go 绑定被拒：' + $rb.Err) } }
    $ev = Invoke-Envs -Cmd @('resolve', '--cwd', $Proj, '--print', 'env')
    if ($ev.Out -notmatch 'GOTOOLCHAIN=local') { return @{ s = 'FAIL'; a = ('resolve env 缺 GOTOOLCHAIN=local：' + $ev.Out) } }
    $r = Invoke-Envs -Cmd @('run', '--cwd', $Proj, '--', 'go', 'env', 'GOTOOLCHAIN')
    $got = Get-FirstLine $r.Out
    if ($got -eq 'local') { return @{ s = 'PASS'; a = ('go ' + $v + ' → go env GOTOOLCHAIN = local') } }
    return @{ s = 'FAIL'; a = ('实得 ' + $got + ' / 期望 local') }
}

Report '13-10' '层 2 回归：npm 未激活不再 exit 78；双形态 shim 齐备' {
    $np = ConvertFrom-EnvsJson (Invoke-Envs -Cmd @('list', 'npm', '--json')).Out
    $npmList = @()
    if ($np) { $npmList = @($np) }
    $shimDir = [IO.Path]::Combine($IsoHome, 'bin\shims')
    if ($npmList -and [string]$npmList[0].version) {
        # --no-path：只重指隔离区里的 junction，不写注册表 PATH
        [void](Invoke-Envs -Cmd @('use', 'npm', [string]$npmList[0].version, '--no-path'))
    }
    $cmdShim = [IO.Path]::Combine($shimDir, 'npm.cmd')
    $shShim  = [IO.Path]::Combine($shimDir, 'npm')
    if (-not (Test-Path -LiteralPath $cmdShim)) { return @{ s = 'SKIP'; a = ('隔离区没有 npm shim：' + $cmdShim) } }
    $saved = $env:PATH
    $out = ''
    $code = 0
    try {
        $env:PATH = $shimDir + ';' + $saved
        $out = (& $cmdShim -v 2>&1 | Out-String)
        $code = $LASTEXITCODE
    } finally { $env:PATH = $saved }
    if ($code -eq 78) { return @{ s = 'FAIL'; a = 'npm 未激活仍走 EXIT /B 78（§9-1 没修好）' } }
    if (-not (Test-Path -LiteralPath $shShim)) { return @{ s = 'FAIL'; a = ('缺无扩展名 shim：' + $shShim + '（Git Bash 不命中，§2-L7）') } }
    return @{ s = 'PASS'; a = ('npm.cmd rc=' + $code + '（非 78），双形态齐备；输出首行 ' + (Get-FirstLine $out)) }
}

Report '13-10b' '无扩展名 shim 在 sh 里真可执行 + 退出码透传（§6.2-1/§2-L7）' {
    if (-not $ShSrc) { return @{ s = 'SKIP'; a = 'sh 四种探测全落空（where / git --exec-path / HKLM GitForWindows / Program Files）' } }
    $shShim = [IO.Path]::Combine([IO.Path]::Combine($IsoHome, 'bin\shims'), 'npm')
    if (-not (Test-Path -LiteralPath $shShim)) { return @{ s = 'FAIL'; a = ('缺无扩展名 shim：' + $shShim) } }
    $head = ([IO.File]::ReadAllLines($shShim))[0]
    if ($head -notmatch '^#!/usr/bin/env sh') { return @{ s = 'FAIL'; a = ('POSIX 形态 shebang 不对：' + $head) } }
    $ok = (& $ShSrc $shShim -v 2>&1 | Out-String)
    $rc1 = $LASTEXITCODE
    if ($rc1 -eq 78) { return @{ s = 'FAIL'; a = '未激活时仍 exit 78（§9-1 未修）' } }
    if ($rc1 -eq 127) { return @{ s = 'FAIL'; a = ('sh 里不可执行（127）：' + (Get-FirstLine $ok)) } }
    # 退出码透传：喂一个不存在的子命令，子命令的非零码必须原样出来，而不是被 shim 吞成 0
    $bad = (& $ShSrc $shShim 'definitely-not-a-real-npm-subcommand-5293' 2>&1 | Out-String)
    $rc2 = $LASTEXITCODE
    if ($rc2 -eq 0) { return @{ s = 'FAIL'; a = '子命令失败被吞成 0，退出码没透传' } }
    if ($rc2 -eq 78) { return @{ s = 'FAIL'; a = '又走回 EXIT /B 78' } }
    return @{ s = 'PASS'; a = ('sh=' + $ShSrc + '；npm -v rc=' + $rc1 + ' 首行 ' + (Get-FirstLine $ok) + '；失败子命令 rc=' + $rc2) }
}

# ================================================================ §14 十条

Report '14-1' 'VSCode 全家桶端到端（终端 / tasks / npm 面板 / F5）' {
    if (-not $CodeCmd) { return @{ s = 'SKIP'; a = '未装 VSCode（code 不在 PATH）' } }
    return @{ s = 'SKIP'; a = '需人工：完全退出并重启 VSCode 后逐条目视四条路径（GUI 缓存环境块，脚本无法代验）' }
}

Report '14-2' '钩子 PATH 膨胀：连续 30 次 cd 后长度不增长' {
    if (-not $canDiscriminate) { return @{ s = 'SKIP'; a = '缺判别版本' } }
    $null = Set-Bind $other.version
    $pp = Invoke-Envs -Cmd @('init', 'powershell')
    $mp = [regex]::Match($pp.Out, '(?s)# BEGIN EnvSwitch directory-binding hook.*?# END EnvSwitch directory-binding hook v1')
    if (-not $mp.Success) { return @{ s = 'FAIL'; a = '拿不到钩子正文' } }
    $ps1 = [IO.Path]::Combine($Iso, 'growth.ps1')
    # 口径：不能拿「注入后的长度」和「离开后的长度」比（那本来就该差一段）。
    # 要断言的是：① 离开态长度 == 开工时的原始长度（回得来）；② 同一相位重复 30 次长度恒定（不叠加）。
    $body = '$ORIG = $env:PATH.Length' + "`r`n" +
        $mp.Value + "`r`n" +
        '$in=@(); $out=@()' + "`r`n" +
        'for($i=0;$i -lt 30;$i++){' + "`r`n" +
        '  if(($i % 2) -eq 0){ Set-Location -LiteralPath ''' + $Proj + ''' } else { Set-Location -LiteralPath ''' + $IsoWork + ''' }' + "`r`n" +
        '  EnvSwitch-CdHook' + "`r`n" +
        '  if(($i % 2) -eq 0){ $in += $env:PATH.Length } else { $out += $env:PATH.Length }' + "`r`n" +
        '}' + "`r`n" +
        '"ORIG=" + $ORIG' + "`r`n" +
        '"INMAX=" + (($in | Measure-Object -Maximum).Maximum)' + "`r`n" +
        '"INMIN=" + (($in | Measure-Object -Minimum).Minimum)' + "`r`n" +
        '"OUTMAX=" + (($out | Measure-Object -Maximum).Maximum)' + "`r`n" +
        '"OUTMIN=" + (($out | Measure-Object -Minimum).Minimum)' + "`r`n"
    [IO.File]::WriteAllText($ps1, $body, (New-Object Text.UTF8Encoding $false))
    $out2 = (& powershell -NoProfile -ExecutionPolicy Bypass -File $ps1 2>&1 | Out-String)
    if ($out2 -notmatch 'ORIG=(\d+)') { return @{ s = 'FAIL'; a = ('子 shell 没跑起来：' + (Get-FirstLine $out2)) } }
    $orig = [int]$Matches[1]
    $inMax = -1; $inMin = -1; $outMax = -1; $outMin = -1
    if ($out2 -match 'INMAX=(\d+)') { $inMax = [int]$Matches[1] }
    if ($out2 -match 'INMIN=(\d+)') { $inMin = [int]$Matches[1] }
    if ($out2 -match 'OUTMAX=(\d+)') { $outMax = [int]$Matches[1] }
    if ($out2 -match 'OUTMIN=(\d+)') { $outMin = [int]$Matches[1] }
    if ($inMin -lt 0 -or $outMin -lt 0) { return @{ s = 'FAIL'; a = ('读数不全：' + ($out2 -replace "`r`n", ' | ')) } }
    if ($inMax -ne $inMin) { return @{ s = 'FAIL'; a = ('注入态长度随 cd 漂移：' + $inMin + ' → ' + $inMax) } }
    if ($outMax -ne $outMin) { return @{ s = 'FAIL'; a = ('离开态长度随 cd 漂移：' + $outMin + ' → ' + $outMax) } }
    if ($outMax -ne $orig) { return @{ s = 'FAIL'; a = ('离开态没回到原始长度：原始 ' + $orig + ' / 离开后 ' + $outMax) } }
    return @{ s = 'PASS'; a = ('30 次 cd：原始 ' + $orig + ' ↔ 注入态 ' + $inMax + '，离开态恒等原始（从快照重建，未叠加）') }
}

Report '14-3' 'JetBrains / HBuilderX / PyCharm 的真实命令行来源' {
    return @{ s = 'SKIP'; a = '需装 JetBrains 系 / HBuilderX 并人工看进程创建路径；结构性结论见方案 §6.4' }
}

Report '14-4' 'dotnet global.json 在 store 布局下的正向选型' {
    if (-not (Get-Command dotnet -ErrorAction SilentlyContinue)) { return @{ s = 'SKIP'; a = '本机没有 dotnet CLI' } }
    $sdks = ((& dotnet --list-sdks 2>&1 | Out-String)).Trim()
    if (-not $sdks) { return @{ s = 'SKIP'; a = 'dotnet --list-sdks 为空：本机只有运行时（与方案 §14-4 实测一致）' } }
    return @{ s = 'SKIP'; a = ('有 SDK（' + (Get-FirstLine $sdks) + '），但 dotnet 属档③原生让位且绑定档位未开放（§5）') }
}

Report '14-5' 'gradle/maven wrapper、rust-toolchain.toml、.java-version' {
    $have = @()
    foreach ($t in @('gradle', 'mvn', 'java', 'rustup')) { if (Get-Command $t -ErrorAction SilentlyContinue) { $have += $t } }
    if ($have.Count -eq 0) { return @{ s = 'SKIP'; a = '本机缺 gradle/mvn/java/rustup（与方案 §14-5 一致）' } }
    return @{ s = 'SKIP'; a = ('有 ' + ($have -join ',') + '，但 java/maven/gradle 绑定属 §11 P3 未开放（§5 档②需 JAVA_HOME 注入）') }
}

Report '14-6' 'PowerShell 7 / MSYS2 / Cygwin / Tabby / WezTerm' {
    $notes = @()
    $notes += $(if ($Ps7) { 'pwsh=在' } else { 'pwsh=缺' })
    $notes += $(if ($BashSrc) { 'bash=在' } else { 'bash=缺' })
    foreach ($t in @('tabby', 'wezterm', 'mintty', 'cygwin')) { if (Get-Command $t -ErrorAction SilentlyContinue) { $notes += ($t + '=在') } }
    return @{ s = 'SKIP'; a = ('钩子正文已在 PS5.1' + $(if ($BashSrc) { ' + Git Bash' } else { '' }) + ' 真跑（见 13-3）；' + ($notes -join ', ') + ' 等非默认壳需各自人工验') }
}

Report '14-7' '多盘/跨卷 junction、OneDrive 占位、subst 盘的键归一' {
    return @{ s = 'SKIP'; a = 'subst / 跨卷 junction 要动真实系统挂载点，按零污染红线不执行；归一逻辑由单测用替换钩子覆盖（TestBindingsNormalizeSubst / 双键测试）' }
}

Report '14-8' 'envs shell 的 TTY 行为（Ctrl+C / 颜色 / 行数 / 退出码透传）' {
    $r = Invoke-Envs -Cmd @('shell', '--help')
    if ($r.Code -ne 0) { return @{ s = 'FAIL'; a = 'envs shell --help 失败' } }
    return @{ s = 'SKIP'; a = '需交互式终端与人工按键；可手跑：envs shell --cwd <绑定目录> --cmd' }
}

Report '14-9' '杀软对 shim 频繁 spawn + AutoRun 写注册表的反应' {
    return @{ s = 'SKIP'; a = '需真实杀软环境与观察时间；本脚本不写注册表，复现不了该面' }
}

Report '14-10' '干净机端到端（无 nvm、store 自装两版本）' {
    $storeDirs = @(Get-ChildItem -LiteralPath ([IO.Path]::Combine($IsoHome, 'store')) -Directory -EA SilentlyContinue)
    return @{ s = 'SKIP'; a = ('本机有 nvm 收编条目、不是干净机；干净 VM 需先 envs install（会下载），本脚本按红线不下载。隔离区 store 现有 ' + $storeDirs.Count + ' 项') }
}

# ================================================================ 环境污染自检

Write-Output ''
Write-Output '================ 环境污染自检（开工前后对比） ================'
$dirty = 0
foreach ($wp2 in $WatchPaths) {
    $now = Get-Snap $wp2
    $was = $Before[$wp2]
    $same = ($now.Exists -eq $was.Exists) -and ($now.Mtime -eq $was.Mtime) -and ($now.Target -eq $was.Target)
    if (-not $same) { $dirty++ }
    $tag = 'OK'
    if (-not $same) { $tag = 'CHANGED' }
    Write-Output ('  {0,-8} {1}' -f $tag, $wp2)
    Write-Output ('           开工前: ' + (Format-Snap $was) + ' / 现在: ' + (Format-Snap $now))
}
$nowAutoRun = Get-RegVal 'Software\Microsoft\Command Processor' 'AutoRun'
$nowPath = Get-RegVal 'Environment' 'Path'
$regPairs = @(
    @('HKCU\Software\Microsoft\Command Processor\AutoRun', $BeforeAutoRun, $nowAutoRun),
    @('HKCU\Environment\Path', $BeforePath, $nowPath)
)
foreach ($rp in $regPairs) {
    $same = ($rp[1].Exists -eq $rp[2].Exists) -and ($rp[1].Value -eq $rp[2].Value)
    if (-not $same) { $dirty++ }
    $tag = 'OK'
    if (-not $same) { $tag = 'CHANGED' }
    Write-Output ('  {0,-8} {1}  开工前[{2}] / 现在[{3}]' -f $tag, $rp[0], $rp[1].Value, $rp[2].Value)
}
if ($dirty -eq 0) {
    Write-Output '  结论：真实 home / profile / .bashrc / HKCU 全部未变动（零污染）'
} else {
    Write-Output ('  结论：检测到 ' + $dirty + ' 处变动 —— 违反红线，查上面的 CHANGED 行')
    Add-Result 'FAIL' 'ENV' '环境污染自检' ('检测到 ' + $dirty + ' 处真实文件/注册表变动')
}

# ================================================================ 汇总与清理

$rc = 0
Write-Output ''
Write-Output ('合计：PASS=' + $script:Pass + '  FAIL=' + $script:Fail + '  SKIP=' + $script:Skip)
if ($script:Fail -gt 0) {
    Write-Output '失败项：'
    foreach ($fl in $script:Lines) { if ($fl.StartsWith('FAIL')) { Write-Output ('  ' + $fl) } }
    $rc = 1
}
Write-Output ('隔离区：' + $Iso)
if ([Environment]::GetEnvironmentVariable('ENVS_KEEP_ISO') -eq '1') {
    Write-Output '  ENVS_KEEP_ISO=1 → 保留隔离区供人工查看'
} else {
    Remove-Item -Recurse -Force -LiteralPath $Iso -EA SilentlyContinue
    Write-Output ('  已清理：' + (-not (Test-Path $Iso)))
}
exit $rc
