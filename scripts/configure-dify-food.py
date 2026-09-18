"""Run inside the local Dify API container; use its workflow service, not raw SQL writes.

Input /tmp/lifed-bootstrap.json, private output /tmp/lifed-result.json.
Backs up the original draft before configuring and publishing the food workflow.
"""
import json
import os
from pathlib import Path

from app import app
from extensions.ext_database import db
from sqlalchemy import select
from models.model import App, ApiToken
from models.account import Account
from models.workflow import Workflow
from models.enums import ApiTokenType
from services.workflow_service import WorkflowService
from graphon.variables import StringVariable, SecretVariable

config = json.loads(Path('/tmp/lifed-bootstrap.json').read_text())
with app.app_context():
    session = db.session()
    application = session.get(App, config['app_id'])
    assert application is not None and application.mode == 'workflow'
    draft = session.scalar(select(Workflow).where(Workflow.app_id == application.id, Workflow.version == 'draft'))
    account = session.get(Account, draft.created_by)
    assert account is not None
    backup = Path('/tmp/lifed-original-draft.json')
    if not backup.exists():
        backup.write_text(json.dumps({'graph': draft.graph_dict, 'features': json.loads(draft.features),
                                     'environment_variables': [v.model_dump(mode='json') for v in draft.environment_variables]}))
        backup.chmod(0o600)
    graph = draft.graph_dict
    fields = ['request', 'delivery_address', 'budget_aud', 'restaurant_id', 'item_ids', 'quote_id', 'purchase_confirmation']
    code = '''import json
def main(request, delivery_address, budget_aud, restaurant_id, item_ids, quote_id, purchase_confirmation):
    return {
      "search_body": json.dumps({"request": request, "delivery_address": delivery_address, "budget_aud": budget_aud}),
      "quote_body": json.dumps({"restaurant_id": restaurant_id, "item_ids": item_ids, "delivery_address": delivery_address, "budget_aud": budget_aud}),
      "order_body": json.dumps({"quote_id": quote_id, "confirmation": purchase_confirmation})
    }
'''
    serializer = {'id': 'serialize_food_input', 'type': 'custom', 'position': {'x': 380, 'y': 282},
                  'data': {'type': 'code', 'title': 'Encode food request as JSON', 'desc': 'Escape user text before HTTP transmission.',
                           'variables': [{'variable': name, 'value_selector': ['start', name]} for name in fields],
                           'code_language': 'python3', 'code': code,
                           'outputs': {name: {'type': 'string', 'children': None} for name in ['search_body', 'quote_body', 'order_body']}}}
    graph['nodes'] = [n for n in graph['nodes'] if n['id'] not in [serializer['id'], 'serialize-food-input']] + [serializer]
    graph['edges'] = [e for e in graph['edges'] if e['id'] not in ['start-router', 'start-serialize', 'serialize-router']]
    for edge_id, source, target, source_type, target_type in [('start-serialize', 'start', serializer['id'], 'start', 'code'),
                                                            ('serialize-router', serializer['id'], 'operation-router', 'code', 'if-else')]:
        graph['edges'].append({'id': edge_id, 'source': source, 'target': target, 'type': 'custom',
                               'sourceHandle': 'source', 'targetHandle': 'target',
                               'data': {'sourceType': source_type, 'targetType': target_type, 'isInLoop': False, 'isInIteration': False}})
    for node in graph['nodes']:
        d = node['data']
        if node['id'] == 'operation-router':
            node['position'] = {'x': 740, 'y': 282}
        if d['type'] != 'http-request':
            continue
        d['params'] = ''
        d['retry_config'] = {'retry_enabled': False, 'max_retries': 0, 'retry_interval': 1000}
        name = {'search-http': 'search_body', 'quote-http': 'quote_body', 'order-http': 'order_body'}.get(node['id'])
        if name:
            d['body'] = {'type': 'raw-text', 'data': [{'key': '', 'type': 'text', 'value': '{{#serialize_food_input.' + name + '#}}'}]}
        else:
            d['body'] = {'type': 'none', 'data': []}
    variables = [v for v in draft.environment_variables if v.name not in ['DOORDASH_ADAPTER_URL', 'DOORDASH_ADAPTER_TOKEN']]
    variables += [StringVariable(name='DOORDASH_ADAPTER_URL', value=config['adapter_url']),
                  SecretVariable(name='DOORDASH_ADAPTER_TOKEN', value=config['adapter_token'])]
    service = WorkflowService()
    service.sync_draft_workflow(app_model=application, graph=graph, features=json.loads(draft.features),
                                unique_hash=draft.unique_hash, account=account, environment_variables=variables,
                                conversation_variables=draft.conversation_variables, session=session)
    published = service.publish_workflow(session=session, app_model=application, account=account,
                                        marked_name='Spider lifed food', marked_comment='Typed food API, JSON serialization, no HTTP mutation retries.')
    session.flush()
    application.workflow_id = published.id
    application.enable_api = True
    token = session.scalar(select(ApiToken).where(ApiToken.app_id == application.id, ApiToken.type == ApiTokenType.APP))
    if token is None:
        token = ApiToken(app_id=application.id, tenant_id=application.tenant_id, type=ApiTokenType.APP,
                         token=ApiToken.generate_api_key('app-', 24, session=session))
        session.add(token)
    session.commit()
    output = Path('/tmp/lifed-result.json')
    output.write_text(json.dumps({'api_key': token.token, 'workflow_id': published.id, 'app_id': application.id}))
    output.chmod(0o600)
    print('DoorDash workflow configured and published. Credentials written to private output file.')
