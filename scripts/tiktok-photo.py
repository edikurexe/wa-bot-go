#!/usr/bin/env python3
"""Download TikTok slideshow/photo images using gallery-dl."""
import sys
import json
import os
import subprocess
import tempfile
import glob

def download_photos(url, output_dir):
    """Download TikTok photo/slideshow images to output_dir.
    Returns JSON with list of image paths and title."""
    os.makedirs(output_dir, exist_ok=True)
    
    try:
        # Use gallery-dl to download images
        result = subprocess.run(
            [
                os.path.expanduser('~/.local/bin/gallery-dl'),
                '--dest', output_dir,
                '--filename', '{num:>02}.{extension}',
                '--directory', '.',  # flat directory
                '--no-mtime',
                '-o', 'browser=false',
                url
            ],
            capture_output=True, text=True, timeout=60
        )
        
        # Find downloaded images
        images = sorted(glob.glob(os.path.join(output_dir, '*.jpg')) +
                       glob.glob(os.path.join(output_dir, '*.jpeg')) +
                       glob.glob(os.path.join(output_dir, '*.png')) +
                       glob.glob(os.path.join(output_dir, '*.webp')))
        
        if not images:
            # Maybe gallery-dl put them in a subdirectory
            images = sorted(glob.glob(os.path.join(output_dir, '**', '*.jpg'), recursive=True) +
                          glob.glob(os.path.join(output_dir, '**', '*.jpeg'), recursive=True) +
                          glob.glob(os.path.join(output_dir, '**', '*.png'), recursive=True) +
                          glob.glob(os.path.join(output_dir, '**', '*.webp'), recursive=True))
        
        if images:
            print(json.dumps({
                'ok': True,
                'type': 'photos',
                'images': images,
                'count': len(images)
            }))
        else:
            # No images found - maybe it's actually a video or download failed
            stderr = result.stderr[:500] if result.stderr else ''
            print(json.dumps({
                'ok': False,
                'error': f'No images found. stderr: {stderr}'
            }))
    except subprocess.TimeoutExpired:
        print(json.dumps({'ok': False, 'error': 'Download timeout'}))
    except Exception as e:
        print(json.dumps({'ok': False, 'error': str(e)[:200]}))


def check_type(url):
    """Check if a TikTok URL is a photo/slideshow or video.
    Uses gallery-dl metadata extraction."""
    try:
        result = subprocess.run(
            [
                os.path.expanduser('~/.local/bin/gallery-dl'),
                '--dump-json',
                url
            ],
            capture_output=True, text=True, timeout=30
        )
        
        if result.returncode == 0 and result.stdout.strip():
            data = json.loads(result.stdout)
            # gallery-dl returns list of tuples [url, metadata]
            if isinstance(data, list) and len(data) > 0:
                # Check if any entry has image URLs
                has_images = False
                for entry in data:
                    if isinstance(entry, list) and len(entry) >= 2:
                        meta = entry[1] if isinstance(entry[1], dict) else {}
                        ext = meta.get('extension', '')
                        if ext in ('jpg', 'jpeg', 'png', 'webp'):
                            has_images = True
                            break
                
                print(json.dumps({
                    'type': 'photos' if has_images else 'video',
                    'count': len(data) if has_images else 0
                }))
                return
        
        print(json.dumps({'type': 'video', 'count': 0}))
    except Exception as e:
        print(json.dumps({'type': 'unknown', 'error': str(e)[:200]}))


if __name__ == '__main__':
    if len(sys.argv) < 3:
        print(json.dumps({'ok': False, 'error': 'Usage: tiktok-photo.py <download|check> <url> [output_dir]'}))
        sys.exit(1)
    
    cmd = sys.argv[1]
    url = sys.argv[2]
    
    if cmd == 'download':
        output_dir = sys.argv[3] if len(sys.argv) > 3 else tempfile.mkdtemp(prefix='tiktok_')
        download_photos(url, output_dir)
    elif cmd == 'check':
        check_type(url)
    else:
        print(json.dumps({'ok': False, 'error': f'Unknown command: {cmd}'}))
