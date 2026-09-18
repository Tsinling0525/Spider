#!/usr/bin/env python3
"""Read-only live smoke test. Does not create a cart or submit an order."""
import json
import os
import sys
import time
import urllib.error
import urllib.request

base = os.environ.get('SPIDER_LIFE_BASE_URL', 'http://127.0.0.1:8081').rstrip('/')
headers = {'Content-Type': 'application/json'}
key = os.environ.get('SPIDER_LIFE_API_KEY') or os.environ.get('LIFE_API_KEY')
if key:
    headers['Authorization'] = 'Bearer ' + key


def call(body=None):
    req = urllib.request.Request(base + '/life/food', headers=headers,
                                 data=json.dumps(body).encode() if body is not None else None)
    try:
        with urllib.request.urlopen(req, timeout=245) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise SystemExit(f'HTTP {error.code}: {error.read().decode()}') from error


status = call()
for _ in range(30):
    if not status.get("refreshing"):
        break
    time.sleep(1)
    status = call()
if not status.get('ready'):
    raise SystemExit(status.get('message', 'Food service unavailable'))
result = call({'operation': 'search', 'request': ' '.join(sys.argv[1:]) or 'noodles',
               'delivery_address': status.get('delivery_address', ''), 'budget_aud': '40'})
restaurants = result['data']['restaurants']
print(json.dumps({'workflow_run_id': result['workflow_run_id'],
                  'restaurants': len(restaurants),
                  'menu_items': sum(len(r['items']) for r in restaurants)}, indent=2))
