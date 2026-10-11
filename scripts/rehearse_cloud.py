#!/usr/bin/env python3
"""Bounded live rehearsal against this project's deployed application API.

Requires running instances and an enabled monitor. Creates synthetic exam data
and stops the active demo EC2 instance through the authenticated cockpit API.
Never changes the AWS plan or reads project login credentials.
"""
import json
import pathlib
import secrets
import time
import urllib.error
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
outputs = json.loads((ROOT / '.demo/aws-output.json').read_text())
API = outputs['api_url']['value'].rstrip('/')
ADMIN = outputs['admin_token']['value']
evidence = {'mode': 'aws', 'region': outputs['region']['value'], 'api_url': API}

def request(method, path, payload=None, token=None, admin=False):
    # Stay below the faculty project's three-request/second API limit.
    time.sleep(0.4)
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if admin:
        headers['x-admin-token'] = ADMIN
    req = urllib.request.Request(API + path,
        data=None if payload is None else json.dumps(payload).encode(),
        headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            body = {'detail': raw.decode(errors='replace')[:200]}
        return response.code, body

def require(condition, message):
    if not condition:
        raise RuntimeError(message)

status_code, baseline = request('GET', '/continuum/status', admin=True)
require(status_code == 200, f'Cockpit not ready: HTTP {status_code}')
require(all(node['healthy'] for node in baseline['backends']), 'Both backends must be ready')
require(baseline['active_backend'] == 'A', 'Reset/fail back to A before this rehearsal')
evidence['initial_epoch'] = baseline['epoch']
code, _ = request('GET', '/continuum/status')
require(code == 403, 'Status must reject requests without the admin token')

code, blip = request('POST', '/continuum/action', {'action': 'blip', 'backend': 'A'}, admin=True)
require(code == 200 and blip.get('monitor_started') is True, 'Transient-fault monitor did not start')
confirmation_window = (baseline['policy']['confirmation_probes'] - 1) * baseline['policy']['probe_interval_ms']
blip_duration = blip.get('fault_expires_in_ms', 0)
require(0 < blip_duration < confirmation_window, 'Transient fault must expire before sustained-failure confirmation')
time.sleep(blip_duration / 1000 + 8)
code, after_blip = request('GET', '/continuum/status', admin=True)
require(code == 200 and all(node['healthy'] for node in after_blip['backends']), 'Transient fault did not clear')
require(after_blip['active_backend'] == 'A' and after_blip['epoch'] == baseline['epoch'], 'Transient fault caused an unnecessary promotion')
evidence['transient_fault'] = {'request': blip, 'active_backend': after_blip['active_backend'], 'epoch': after_blip['epoch'], 'events': after_blip['events']}

known_events = {item['id'] for item in after_blip['events']}
for action, backend in [('unsafe_standby', 'B'), ('partial', 'A')]:
    code, _ = request('POST', '/continuum/action', {'action': action, 'backend': backend}, admin=True)
    require(code == 200, f'Capability fault failed: {action}, HTTP {code}')
code, unsafe = request('GET', '/continuum/status', admin=True)
require(code == 200 and all(node['liveness'] and not node['ready'] for node in unsafe['backends']), 'Capability fault must preserve liveness while failing readiness')
code, _ = request('POST', '/continuum/action', {'action': 'verify'}, admin=True)
require(code == 200, 'Unsafe-standby verification was not accepted')
deadline = time.monotonic() + 30
suppressed = None
while time.monotonic() < deadline:
    time.sleep(3)
    code, observation = request('GET', '/continuum/status', admin=True)
    if code != 200:
        continue
    require(observation['active_backend'] == 'A' and observation['epoch'] == baseline['epoch'], 'Unready standby was promoted')
    fresh = [item for item in observation['events'] if item['id'] not in known_events and item['type'] == 'suppressed']
    if fresh:
        suppressed = fresh[0]
        break
require(suppressed is not None, 'Unsafe promotion was not recorded as suppressed')
evidence['unsafe_standby'] = {'backends': unsafe['backends'], 'epoch': unsafe['epoch'], 'suppressed': suppressed}
code, cleared = request('POST', '/continuum/action', {'action': 'reset'}, admin=True)
require(code == 200 and all(node['healthy'] for node in cleared.get('backends', [])) and len(cleared.get('backends', [])) == 2, 'Fault reset did not restore healthy backends')
print('Transient fault suppressed; alive-but-unready backends refused promotion.', flush=True)

code, registration = request('POST', '/auth/register', {
    'email': 'rehearsal-' + uuid.uuid4().hex[:12] + '@continuum.test',
    'password': secrets.token_urlsafe(24), 'name': 'Synthetic rehearsal student'})
require(code == 201, f'Synthetic registration failed: HTTP {code}')
token = registration['access_token']
code, _ = request('GET', '/exam/cs101-midterm', token=token)
require(code == 200, f'Exam start failed: HTTP {code}')
code, _ = request('POST', '/continuum/action', {'action': 'lost_response', 'backend': 'A'}, admin=True)
require(code == 200, f'Lost-response injection failed: HTTP {code}')

payload = {'request_id': 'cloud-' + uuid.uuid4().hex, 'answers': {'0': 1, '1': 0}}
evidence['request_id'] = payload['request_id']
code, _ = request('POST', '/exam/cs101-midterm/submit', payload, token=token)
require(code == 503, f'Expected a lost receipt after commit: HTTP {code}')
evidence['lost_response_status'] = code
code, committed = request('GET', '/exam/cs101-midterm/result', token=token)
require(code == 200 and committed.get('request_id') == payload['request_id'], 'Commit missing before failure')
evidence['committed_at'] = committed.get('committed_at')

fault_at = time.time()
evidence['fault_injected_at_ms'] = int(fault_at * 1000)
code, _ = request('POST', '/continuum/action', {'action': 'crash', 'backend': 'A'}, admin=True)
require(code == 200, f'EC2 stop action failed: HTTP {code}')
print('Active EC2 stop requested; waiting for scheduled verification and recovery.', flush=True)

deadline = time.monotonic() + 150
recovered = None
while time.monotonic() < deadline:
    time.sleep(4)
    code, observation = request('GET', '/continuum/status', admin=True)
    if code == 200 and observation['active_backend'] == 'B' and observation['epoch'] > baseline['epoch']:
        if observation['metrics'].get('reconciled') is True:
            recovered = observation
            break
require(recovered is not None, 'No verified B recovery and reconciliation within 150 seconds')
code, health = request('GET', '/health/deep')
require(code == 200 and health.get('healthy') is True, 'Public exam route failed after promotion')
evidence['fault_to_observed_recovery_ms'] = int((time.time() - fault_at) * 1000)
evidence['final_epoch'] = recovered['epoch']
evidence['metrics'] = recovered['metrics']
evidence['events'] = recovered['events']

code, receipt = request('POST', '/exam/cs101-midterm/submit', payload, token=token)
require(code == 200 and receipt.get('status') == 'duplicate' and receipt.get('request_id') == payload['request_id'], 'Replay did not recover the same receipt')
require(receipt.get('committed_at') == evidence['committed_at'], 'Commit timestamp changed on replay')
evidence['replayed_receipt'] = receipt
changed = {'request_id': payload['request_id'], 'answers': {'0': 0}}
code, _ = request('POST', '/exam/cs101-midterm/submit', changed, token=token)
require(code == 409, 'Changed payload was accepted under the original request ID')
evidence['changed_payload_status'] = code
known_events = {item['id'] for item in recovered['events']}
code, repeated = request('POST', '/continuum/action', {'action': 'reconcile'}, admin=True)
require(code == 200 and repeated.get('accepted') is True, 'Manual reconciliation was not accepted asynchronously')
evidence['repeat_reconciliation_request'] = repeated
repeat_event = None
deadline = time.monotonic() + 60
while time.monotonic() < deadline:
    time.sleep(4)
    code, observation = request('GET', '/continuum/status', admin=True)
    if code != 200:
        continue
    fresh = [item for item in observation['events']
             if item['id'] not in known_events and item['type'] in ('reconcile', 'reconcile_failed')]
    if fresh:
        repeat_event = fresh[0]
        break
require(repeat_event is not None and repeat_event['type'] == 'reconcile', 'Manual reconciliation did not complete successfully')
require('processed=0 ' in repeat_event['message'] and 'conflicts=0' in repeat_event['message'], 'Repeated reconciliation created more work or conflicts')
evidence['repeat_reconciliation_event'] = repeat_event

path = ROOT / 'docs/evidence/cloud-rehearsal.json'
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(evidence, indent=2) + '\n')
print(json.dumps({'passed': True, 'fault_to_observed_recovery_ms': evidence['fault_to_observed_recovery_ms'],
                  'final_epoch': evidence['final_epoch'], 'request_id': payload['request_id'],
                  'evidence': str(path)}, indent=2))
