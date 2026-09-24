#!/usr/bin/env python3
"""Export published Spider apps with Dify's own exporter, without credentials.

Requires access to a running Dify 1.17 API container. Does not publish or edit apps.
Review generated YAML before committing: free-text prompts may contain private data.
"""
import argparse
import json
from pathlib import Path
import subprocess

CONTAINER_SCRIPT = r'''
import json
import sys
import yaml
from app import app
from extensions.ext_database import db
from models.model import App
from services.app_dsl_service import AppDslService

specs = json.loads(sys.argv[1])
exports = {}
with app.app_context():
    session = db.session()
    for filename, app_id in specs.items():
        application = session.get(App, app_id)
        if application is None or not application.workflow_id:
            raise RuntimeError(f"{filename}: application missing or not published")
        content = yaml.safe_load(AppDslService.export_dsl(
            app_model=application, session=session, include_secret=False,
            workflow_id=application.workflow_id))
        expected_mode = 'workflow' if filename == 'food-ordering.yml' else 'advanced-chat'
        if content['app']['mode'] != expected_mode:
            raise RuntimeError(f"{filename}: unexpected app mode")
        workflow = content['workflow']
        # Clear both secret and ordinary environment values: ordinary text variables
        # can contain private endpoint/account details too. Keep IDs/types/selectors.
        for variable in workflow.get('environment_variables', []):
            if variable.get('value_type') == 'number':
                variable['value'] = 0
            else:
                variable['value'] = ''
        # Export metadata is not a substitute for the full YAML app envelope.
        exports[filename] = {
            'yaml': yaml.safe_dump(content, allow_unicode=True, sort_keys=False),
            'source_app_id': app_id,
            'source_workflow_id': application.workflow_id,
            'dsl_version': content['version'],
        }
    session.rollback()
print('SPIDER_DSL_EXPORT=' + json.dumps(exports, ensure_ascii=False))
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', default='docker-api-1')
    parser.add_argument('--email-app', required=True)
    parser.add_argument('--food-app', required=True)
    parser.add_argument('--chat-app')
    parser.add_argument('--output-dir', type=Path, default=Path(__file__).resolve().parent)
    args = parser.parse_args()
    specs = {'email-reply-refiner.yml': args.email_app, 'food-ordering.yml': args.food_app}
    if args.chat_app:
        specs['chatbot.yml'] = args.chat_app
    process = subprocess.run(
        ['docker', 'exec', '-i', args.container, '/app/api/.venv/bin/python',
         '-', json.dumps(specs)], input=CONTAINER_SCRIPT, text=True,
        capture_output=True, check=False)
    if process.returncode:
        # Container output can contain configuration values. Do not replay it.
        raise SystemExit('Export failed. Check container, published app IDs and Dify version; no files written.')
    lines = [line for line in process.stdout.splitlines() if line.startswith('SPIDER_DSL_EXPORT=')]
    if len(lines) != 1:
        raise SystemExit('Export did not return one complete result; no files written.')
    exports = json.loads(lines[0].split('=', 1)[1])
    if set(exports) != set(specs):
        raise SystemExit('Export returned unexpected files; no files written.')
    args.output_dir.mkdir(parents=True, exist_ok=True)
    manifest = {}
    for filename, item in exports.items():
        destination = args.output_dir / filename
        destination.write_text(item['yaml'], encoding='utf-8')
        manifest[filename] = {key: value for key, value in item.items() if key != 'yaml'}
        print(f'Exported {filename} (published version; environment values cleared)')
    (args.output_dir / 'manifest.json').write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print('Review prompts, HTTP headers, URLs and conversation defaults before committing.')


if __name__ == '__main__':
    main()
