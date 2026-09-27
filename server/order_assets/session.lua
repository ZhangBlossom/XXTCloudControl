-- The host prepends a validated per-order `config`. No credentials are embedded.
-- Requires a verified device profile and a game-specific cleanup/verify module.
-- Native XXTouch calls require a real-device acceptance test before enabling a profile.
local function read_text(p)
    local f = io.open(p, 'rb')
    if not f then return nil end
    local text = f:read('*a'); f:close(); return text
end
local function write_text(p, text)
    local f = assert(io.open(p .. '.tmp', 'wb'))
    assert(f:write(text)); assert(f:close())
    assert(os.rename(p .. '.tmp', p))
end
local remaining = config.quantity
local stopped = false
local function report(phase, reason)
    write_text(config.report_path, assert(json.encode({
        session_id = config.session_id, phase = phase,
        remaining = remaining, stopped = stopped, error = reason
    })))
end
-- A replay must never reset consumed quota, even if XXTouch reruns the file.
assert(not read_text(config.report_path), 'session already initialized; cleanup required')

local cleanup
local function policy(quota)
    -- Keep the limiter enabled when closing. enabled=false would allow all purchases.
    local value = {enabled = true, allowedPriceQuotas = {[config.price] = quota}}
    -- Clearing app data can replace the container. Resolve its current path.
    local target = cleanup and cleanup.policy_path and cleanup.policy_path(config.bundle_id) or config.policy_path
    plist.write(target, value)
    local actual = plist.read(target)
    assert(type(actual) == 'table' and actual.enabled == true and
        type(actual.allowedPriceQuotas) == 'table' and
        actual.allowedPriceQuotas[config.price] == quota, 'policy readback failed')
    remaining = quota
end
local function stop_media()
    local status = http.post('http://127.0.0.1:' .. tostring(sys.port()) .. '/api/webrtc/stop', 5)
    assert(status and status >= 200 and status < 300, 'remote control stop unconfirmed')
end
local function stop_game()
    app.close(config.bundle_id)
    for _ = 1, 50 do
        if not app.is_running(config.bundle_id) then return end
        sys.msleep(100)
    end
    error('game still running')
end
local function stop_session()
    -- Attempt every action even if an earlier one fails. Only confirmed stops
    -- permit the host to mark the device as waiting for account cleanup.
    local errors = {}
    local function attempt(name, fn)
        local ok = pcall(fn)
        if not ok then errors[#errors + 1] = name end
    end
    attempt('media_stop_unconfirmed', stop_media)
    attempt('game_stop_unconfirmed', stop_game)
    attempt('policy_close_unconfirmed', function() policy(0) end)
    stopped = #errors == 0
    return table.concat(errors, ';')
end
local function close_session()
    local stop_error = stop_session()
    assert(stopped, stop_error)
    if config.manual_test then
        report('failed', 'manual_cleanup_required')
        return
    end
    report('cleaning')
    assert(cleanup and cleanup.clean(config.bundle_id) == true, 'cleanup failed')
    policy(0)
    assert(cleanup.verify(config.bundle_id) == true, 'cleanup verification failed')
    report('closed')
end
local ok, failure = xpcall(function()
    report('preparing')
    if not config.manual_test then
        cleanup = assert(dofile(config.cleanup_script))
        assert(type(cleanup) == 'table' and type(cleanup.clean) == 'function' and
            type(cleanup.verify) == 'function', 'invalid cleanup module')
        assert(cleanup.verify(config.bundle_id) == true, 'device is not clean')
    end
    stop_game()
    policy(config.quantity)
    assert(app.run(config.bundle_id) == 0, 'game launch failed')
    local foreground = false
    for _ = 1, 100 do
        if app.is_running(config.bundle_id) and app.front_bid() == config.bundle_id then
            foreground = true
            break
        end
        sys.msleep(100)
    end
    assert(foreground, 'game_foreground_unconfirmed')
    local deadline = os.time() + config.duration_seconds
    report('ready')
    while os.time() < deadline and not read_text(config.stop_path) do
        local target = cleanup and cleanup.policy_path and cleanup.policy_path(config.bundle_id) or config.policy_path
        local actual = plist.read(target)
        local quota = actual and actual.allowedPriceQuotas and actual.allowedPriceQuotas[config.price]
        assert(actual and actual.enabled == true and type(quota) == 'number' and
            quota >= 0 and quota <= remaining, 'invalid or reset quota')
        remaining = quota
        report('ready')
        sys.msleep(1000)
    end
    close_session()
end, function(err) return tostring(err) end)
if not ok then
    local stop_error = stop_session()
    local reason = failure or 'session_failed'
    if stop_error ~= '' then reason = reason .. ';' .. stop_error end
    pcall(report, 'failed', reason)
end
