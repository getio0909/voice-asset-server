param(
    [Parameter(Mandatory = $true)]
    [string] $OutputPath,

    [string] $Text = 'Welcome to Voice Asset. This is a live speech recognition test.',

    [switch] $Cleanup
)

$ErrorActionPreference = 'Stop'
$resolvedParent = [IO.Path]::GetFullPath((Split-Path -Parent $OutputPath))
if (-not [IO.Directory]::Exists($resolvedParent)) {
    throw 'Output directory does not exist.'
}
$resolvedOutput = [IO.Path]::GetFullPath($OutputPath)
if ([IO.Path]::GetExtension($resolvedOutput) -ne '.wav') {
    throw 'OutputPath must use the .wav extension.'
}
if ($Cleanup) {
    Remove-Item -LiteralPath $resolvedOutput -Force -ErrorAction SilentlyContinue
    return
}

$voice = $null
$stream = $null
$format = $null
try {
    $voice = New-Object -ComObject SAPI.SpVoice
    $stream = New-Object -ComObject SAPI.SpFileStream
    $format = New-Object -ComObject SAPI.SpAudioFormat
    # SpeechAudioFormatType 18 is 16 kHz, 16-bit, mono PCM.
    $format.Type = 18
    $stream.Format = $format
    $stream.Open($resolvedOutput, 3, $false)
    $voice.AudioOutputStream = $stream
    [void] $voice.Speak($Text)
    $stream.Close()

    $header = [IO.File]::ReadAllBytes($resolvedOutput)
    if ($header.Length -le 44 -or
        [BitConverter]::ToInt32($header, 24) -ne 16000 -or
        [BitConverter]::ToInt16($header, 22) -ne 1 -or
        [BitConverter]::ToInt16($header, 34) -ne 16) {
        throw 'Generated fixture is not 16 kHz, 16-bit, mono PCM WAV.'
    }
} finally {
    if ($stream -ne $null) {
        try { $stream.Close() } catch { }
        [void] [Runtime.InteropServices.Marshal]::ReleaseComObject($stream)
    }
    if ($format -ne $null) {
        [void] [Runtime.InteropServices.Marshal]::ReleaseComObject($format)
    }
    if ($voice -ne $null) {
        [void] [Runtime.InteropServices.Marshal]::ReleaseComObject($voice)
    }
}
