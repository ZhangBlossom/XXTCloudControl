"""Execute the real order Lua with simulated XXTouch APIs; never connects to a phone.
Run: python3 -m pip install lupa && python3 tests/order_lua_simulation.py
This checks orchestration, not StoreKit interception or account erasure.
"""
from pathlib import Path
from lupa import LuaRuntime

SCRIPT = (Path(__file__).parents[1] / 'server/order_assets/session.lua').read_text()
SETUP = r'''
config = {session_id='test-session', bundle_id='test.game', price='648', quantity=5,
 policy_path='/policy', report_path='/report', stop_path='/stop', cleanup_script='/cleanup', duration_seconds=360,
 manual_test=scenario=='manual'}
files, reports, writes = {}, {}, {}
clock, ticks, media_stops, game_stops = 0, 0, 0, 0
running, clean_called = false, false
json = {encode=function(v)
 reports[#reports+1] = {phase=v.phase, remaining=v.remaining, session_id=v.session_id, stopped=v.stopped, error=v.error}
 return v.phase
end}
io = {open=function(p, mode)
 if mode == 'rb' and files[p] == nil then return nil end
 return {read=function() return files[p] end,
 write=function(_,v) files[p]=v; return true end, close=function() return true end}
end}
os = {time=function() return clock end, rename=function(a,b) files[b]=files[a]; files[a]=nil;return true end}
plist = {write=function(p,v)
 if scenario=='dynamic_container' then assert(p == (clean_called and '/new-policy' or '/old-policy'), 'stale container policy write') end
 if scenario=='zero_failure' and v.allowedPriceQuotas['648']==0 then return end
 current=v;writes[#writes+1]=v.allowedPriceQuotas['648'] end,
 read=function(p)
 if scenario=='dynamic_container' then assert(p == (clean_called and '/new-policy' or '/old-policy'), 'stale container policy read') end
 return current end}
http = {post=function() media_stops=media_stops+1;return scenario=='media_failure' and 500 or 200 end}
app = {close=function()
 game_stops=game_stops+1
 if scenario=='game_stop_failure' and running then error('close failed') end
 running=false end,
 is_running=function() return running end,
 front_bid=function() return scenario=='foreground_failure' and 'other.game' or config.bundle_id end,
 run=function() running=scenario~='launch_failure';return scenario=='launch_failure' and -1 or 0 end}
sys = {port=function() return 46952 end, msleep=function(ms)
 ticks=ticks+1; clock=clock+ms/1000
 if scenario=='consume' and ticks<=5 then current.allowedPriceQuotas['648']=5-ticks end
 if scenario=='reset' and ticks==1 then current.allowedPriceQuotas['648']=6 end
 if scenario=='stop' then files['/stop']='test-session' end
end}
dofile = function()
 if scenario=='missing_cleanup' then error('missing module') end
 return {clean=function() clean_called=true;return scenario~='cleanup_failure' end,
 verify=function() return true end,
 policy_path=scenario=='dynamic_container' and function() return clean_called and '/new-policy' or '/old-policy' end or nil}
end
'''

def run(scenario):
    lua = LuaRuntime()
    lua.globals().scenario = scenario
    lua.execute(SETUP)
    lua.execute(SCRIPT)
    g = lua.globals()
    phases = [g.reports[i].phase for i in range(1, len(g.reports)+1)]
    assert phases[0] == 'preparing', phases
    assert bool(g.running) == (scenario == 'game_stop_failure'), scenario
    assert g.current.allowedPriceQuotas['648'] == (5 if scenario == 'zero_failure' else 0), scenario
    assert g.media_stops >= 1, scenario
    assert [g.writes[i] for i in range(1,len(g.writes)+1)].count(5) <= 1, 'quota was replenished'
    last = g.reports[len(g.reports)]
    assert last.stopped == (scenario not in ['zero_failure', 'media_failure', 'game_stop_failure']), scenario
    assert last.remaining == (5 if scenario == 'zero_failure' else 0), scenario
    return lua, phases

lua, phases = run('consume')
assert phases[-1] == 'closed' and lua.globals().ticks == 360
assert lua.globals().reports[len(lua.globals().reports)].remaining == 0
# Replaying the same script cannot replenish this session.
before = len(lua.globals().writes)
try:
    lua.execute(SCRIPT)
except Exception as e:
    assert 'already initialized' in str(e)
else:
    raise AssertionError('replayed session initialized twice')
assert len(lua.globals().writes) == before
lua, phases = run('dynamic_container')
assert phases[-1] == 'closed', phases
lua, phases = run('stop')
assert phases[-1] == 'closed' and lua.globals().ticks == 1
for scenario in ['reset', 'cleanup_failure', 'missing_cleanup']:
    _, phases = run(scenario)
    assert phases[-1] == 'failed', (scenario, phases)
for scenario, reason in [('foreground_failure', 'game_foreground_unconfirmed'),
                         ('launch_failure', 'game launch failed'),
                         ('zero_failure', 'policy_close_unconfirmed'),
                         ('media_failure', 'media_stop_unconfirmed'),
                         ('game_stop_failure', 'game_stop_unconfirmed'),
                         ('manual', 'manual_cleanup_required')]:
    lua, phases = run(scenario)
    last = lua.globals().reports[len(lua.globals().reports)]
    assert phases[-1] == 'failed' and reason in last.error, (scenario, last.error)
    if scenario in ['foreground_failure', 'launch_failure']:
        assert 'ready' not in phases, scenario
    if scenario == 'manual':
        assert not lua.globals().clean_called
print('PASS: Lua 360s expiry, foreground readiness, stop, quota monitoring, replay, manual cleanup and independent failure isolation (simulated APIs only)')
