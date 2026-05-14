#!/usr/bin/env python3
"""Helper script for yt-dlp downloads, called from WA bot."""
import sys
import json
import os
import subprocess
import urllib.parse
import urllib.request
import yt_dlp

def download_tiktok_tikwm(url, output):
    """Fallback TikTok downloader for videos that require login in yt-dlp."""
    api = 'https://www.tikwm.com/api/'
    data = urllib.parse.urlencode({'url': url, 'hd': '1'}).encode()
    req = urllib.request.Request(api, data=data, headers={
        'User-Agent': 'Mozilla/5.0',
        'Content-Type': 'application/x-www-form-urlencoded',
    })
    with urllib.request.urlopen(req, timeout=30) as resp:
        payload = json.loads(resp.read().decode('utf-8', 'ignore'))
    if payload.get('code') != 0 or not payload.get('data'):
        raise RuntimeError(payload.get('msg') or 'TikWM API failed')
    item = payload['data']
    # Prefer normal play/wmplay because TikWM hdplay can be ByteVC/bvc2,
    # which WhatsApp/ffmpeg often can't play/decode. play is usually H.264.
    video_url = item.get('play') or item.get('wmplay') or item.get('hdplay')
    if not video_url:
        raise RuntimeError('TikWM video URL not found')
    if video_url.startswith('/'):
        video_url = 'https://www.tikwm.com' + video_url
    vreq = urllib.request.Request(video_url, headers={'User-Agent': 'Mozilla/5.0', 'Referer': 'https://www.tikwm.com/'})
    with urllib.request.urlopen(vreq, timeout=120) as resp, open(output, 'wb') as f:
        while True:
            chunk = resp.read(1024 * 256)
            if not chunk:
                break
            f.write(chunk)
    title = item.get('title') or 'TikTok Video'
    return title

def is_tiktok(url):
    return 'tiktok.com' in url.lower() or 'vt.tiktok.com' in url.lower() or 'vm.tiktok.com' in url.lower()

def download(url, output, max_size=16*1024*1024):
    # Try multiple format selectors from strict to lenient
    # (matching Telegram bot's working config)
    format_selectors = [
        f'bestvideo[ext=mp4]+bestaudio[ext=m4a]/bestvideo+bestaudio/best',
        f'bestvideo+bestaudio/best',
        'best',
        'sd',
    ]
    
    base_opts = {
        'merge_output_format': 'mp4',
        'outtmpl': output,
        'quiet': True,
        'no_warnings': True,
        'noprogress': True,
        'noplaylist': True,
        'geo_bypass': True,
    }
    
    last_error = None
    for fmt in format_selectors:
        opts = {**base_opts, 'format': fmt}
        try:
            with yt_dlp.YoutubeDL(opts) as ydl:
                info = ydl.extract_info(url, download=True)
                title = info.get('title', 'Video')
                
                # Check if output exists (yt-dlp may add extension)
                actual_output = output
                if not os.path.exists(output):
                    # Try common extensions
                    for ext in ['.mp4', '.mkv', '.webm']:
                        candidate = output.rsplit('.', 1)[0] + ext
                        if os.path.exists(candidate):
                            actual_output = candidate
                            break
                
                # Re-encode to h264 if needed (AV1/VP9 not playable on all devices),
                # then compress if still too large for WhatsApp bot limit.
                if os.path.exists(actual_output):
                    actual_output = ensure_h264(actual_output, output)
                    actual_output = ensure_max_size(actual_output, output, max_size)
                
                print(json.dumps({'ok': True, 'title': title}))
                return
        except Exception as e:
            last_error = e
            # Clean up partial download before retry
            for f in [output, output.rsplit('.', 1)[0] + '.mkv', output.rsplit('.', 1)[0] + '.webm']:
                if os.path.exists(f):
                    os.remove(f)
            continue
    
    # TikTok fallback: yt-dlp often needs cookies for age/sensitive-gated posts.
    if is_tiktok(url):
        try:
            title = download_tiktok_tikwm(url, output)
            if os.path.exists(output):
                actual_output = ensure_h264(output, output)
                actual_output = ensure_max_size(actual_output, output, max_size)
                if actual_output != output and os.path.exists(actual_output):
                    os.rename(actual_output, output)
            print(json.dumps({'ok': True, 'title': title}))
            return
        except Exception as e:
            last_error = e

    print(json.dumps({'ok': False, 'error': str(last_error)[:200]}))


def ensure_h264(input_path, desired_output):
    """Re-encode to h264 mp4 if the video uses AV1/VP9/other codec."""
    try:
        # Check codec
        probe = subprocess.run(
            ['ffprobe', '-v', 'quiet', '-select_streams', 'v:0',
             '-show_entries', 'stream=codec_name', '-of', 'csv=p=0', input_path],
            capture_output=True, text=True, timeout=10
        )
        codec = probe.stdout.strip().lower()
        
        if codec in ('h264', 'avc', 'avc1'):
            # Already h264, just make sure it's at desired_output
            if input_path != desired_output:
                os.rename(input_path, desired_output)
            return desired_output
        
        # Need re-encode
        temp_out = desired_output + '.tmp.mp4'
        subprocess.run(
            ['ffmpeg', '-y', '-i', input_path,
             '-c:v', 'libx264', '-preset', 'fast', '-crf', '23',
             '-c:a', 'aac', '-b:a', '128k',
             '-movflags', '+faststart', temp_out],
            capture_output=True, timeout=120
        )
        
        if os.path.exists(temp_out) and os.path.getsize(temp_out) > 0:
            # Clean up original and rename
            if input_path != desired_output:
                os.remove(input_path)
            if os.path.exists(desired_output):
                os.remove(desired_output)
            os.rename(temp_out, desired_output)
            return desired_output
        else:
            # Re-encode failed, use original
            if input_path != desired_output:
                os.rename(input_path, desired_output)
            return desired_output
    except Exception:
        # On any error, just use what we have
        if input_path != desired_output and os.path.exists(input_path):
            os.rename(input_path, desired_output)
        return desired_output

def ensure_max_size(input_path, desired_output, max_size):
    """Compress to H.264/AAC MP4 to fit max_size when possible."""
    try:
        if not os.path.exists(input_path):
            return input_path
        if os.path.getsize(input_path) <= max_size:
            if input_path != desired_output and os.path.exists(input_path):
                os.rename(input_path, desired_output)
            return desired_output

        duration = probe_duration(input_path)
        if duration <= 0:
            raise RuntimeError('durasi video tidak terbaca untuk kompres')

        candidates = []
        # First pass: keep decent quality, calculate bitrate for target size.
        candidates.append(build_compress_args(input_path, desired_output + '.small.mp4', max_size, duration, 720, 64, 0.86, 'veryfast'))
        # Second pass: more aggressive for long/heavy videos.
        candidates.append(build_compress_args(input_path, desired_output + '.tiny.mp4', max_size, duration, 540, 48, 0.80, 'veryfast'))
        # Last resort: 480p and lower audio budget.
        candidates.append(build_compress_args(input_path, desired_output + '.mini.mp4', max_size, duration, 480, 40, 0.76, 'veryfast'))

        best = None
        for args, temp_out in candidates:
            if os.path.exists(temp_out):
                os.remove(temp_out)
            subprocess.run(args, capture_output=True, timeout=240)
            if not os.path.exists(temp_out) or os.path.getsize(temp_out) <= 0:
                continue
            size = os.path.getsize(temp_out)
            if best is None or size < os.path.getsize(best):
                if best and os.path.exists(best):
                    os.remove(best)
                best = temp_out
            else:
                os.remove(temp_out)
            if size <= max_size:
                best = temp_out
                break

        if best and os.path.exists(best):
            if os.path.getsize(best) <= max_size:
                if input_path != desired_output and os.path.exists(input_path):
                    os.remove(input_path)
                if os.path.exists(desired_output):
                    os.remove(desired_output)
                os.rename(best, desired_output)
                return desired_output
            size_mb = os.path.getsize(best) / 1024 / 1024
            os.remove(best)
            raise RuntimeError(f'video masih terlalu besar setelah kompres ({size_mb:.1f} MB), max {max_size/1024/1024:.0f} MB')

        raise RuntimeError('kompres video gagal')
    except RuntimeError:
        raise
    except Exception as e:
        raise RuntimeError(f'kompres video gagal: {e}')


def probe_duration(path):
    probe = subprocess.run(
        ['ffprobe', '-v', 'error', '-show_entries', 'format=duration',
         '-of', 'default=noprint_wrappers=1:nokey=1', path],
        capture_output=True, text=True, timeout=10
    )
    return float((probe.stdout or '0').strip() or 0)


def build_compress_args(input_path, temp_out, max_size, duration, max_width, audio_k, budget_ratio, preset):
    target_total_k = int((max_size * 8 / duration) / 1000 * budget_ratio)
    # Allow very low bitrate for long clips so ffmpeg still has a chance to fit 16 MB.
    video_k = max(120, target_total_k - audio_k)
    scale = f'scale=trunc(min({max_width},iw)/2)*2:-2'
    return [
        'ffmpeg', '-y', '-i', input_path,
        '-vf', scale,
        '-c:v', 'libx264', '-preset', preset,
        '-b:v', f'{video_k}k', '-maxrate', f'{video_k}k', '-bufsize', f'{max(video_k*2, 240)}k',
        '-c:a', 'aac', '-b:a', f'{audio_k}k',
        '-movflags', '+faststart', temp_out,
    ], temp_out

def get_title(url):
    opts = {'quiet': True, 'no_warnings': True, 'noplaylist': True, 'geo_bypass': True}
    try:
        with yt_dlp.YoutubeDL(opts) as ydl:
            info = ydl.extract_info(url, download=False)
            print(info.get('title', 'Video'))
    except:
        print('Video')

if __name__ == '__main__':
    cmd = sys.argv[1]
    url = sys.argv[2]
    output = sys.argv[3] if len(sys.argv) > 3 else '/tmp/video.mp4'
    
    if cmd == 'download':
        download(url, output)
    elif cmd == 'title':
        get_title(url)
