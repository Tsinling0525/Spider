#!/usr/bin/env python3
"""Loopback-only WAV transcription for Spider; no cloud speech credentials."""
import io
import json
import logging
import os
import threading
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from faster_whisper import WhisperModel

logging.basicConfig(level=logging.INFO, format='%(asctime)s %(message)s')
model = WhisperModel(os.environ.get('SPIDER_ASR_MODEL', 'small'), device='cpu',
                     compute_type='int8', cpu_threads=4,
                     download_root=os.environ.get('SPIDER_ASR_MODEL_DIR', 'data/device/models'))
lock = threading.Lock()

class Handler(BaseHTTPRequestHandler):
    def reply(self, status, value):
        data = json.dumps(value, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        self.reply(200 if self.path == '/health' else 404,
                   {'ready': True} if self.path == '/health' else {'error': 'not found'})

    def do_POST(self):
        self.connection.settimeout(30)
        if self.path != '/transcribe':
            self.reply(404, {'error': 'not found'})
            return
        try:
            size = int(self.headers.get('Content-Length', '0'))
        except ValueError:
            size = 0
        if self.headers.get('Content-Type') != 'audio/wav' or not 44 <= size <= 700000:
            self.reply(400, {'error': '16 kHz mono WAV required, at most 20 seconds'})
            return
        if not lock.acquire(blocking=False):
            self.reply(503, {'error': 'recognizer busy'})
            return
        try:
            raw = self.rfile.read(size)
            if len(raw) != size:
                raise ValueError('incomplete audio')
            with wave.open(io.BytesIO(raw)) as audio:
                if audio.getnchannels() != 1 or audio.getframerate() != 16000 or audio.getsampwidth() != 2:
                    raise ValueError('invalid audio format')
                # FFmpeg streaming WAV has an unknown length in the header;
                # validate the actual PCM bytes instead of that sentinel.
                pcm = audio.readframes(320001)
                if not 0 < len(pcm) <= 640000:
                    raise ValueError('invalid audio duration')
            segments, _ = model.transcribe(io.BytesIO(raw), language=os.environ.get('SPIDER_ASR_LANGUAGE', 'zh'),
                                            beam_size=5, vad_filter=True,
                                            condition_on_previous_text=False,
                                            initial_prompt='以下是普通话语音，请使用简体中文转写。')
            text = ''.join(segment.text for segment in segments).strip()
            logging.info('transcribed %d audio bytes into %d characters', len(raw), len(text))
            self.reply(200, {'text': text})
        except (ValueError, wave.Error, EOFError):
            self.reply(400, {'error': 'invalid WAV'})
        except Exception:
            logging.exception('transcription failed')
            self.reply(500, {'error': 'transcription failed'})
        finally:
            lock.release()

logging.info('local speech recognition ready on 127.0.0.1:8085')
ThreadingHTTPServer(('127.0.0.1', 8085), Handler).serve_forever()
