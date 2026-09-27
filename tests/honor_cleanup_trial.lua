-- One-device acceptance experiment, NOT an automatic clean/verify module.
-- Run only while the device is reserved and no order session is running.
-- Recovery data stays outside XXTouch's HTTP-served scripts directory.
local bundle = 'com.levelinfinite.sgameGlobal'
local resource = 'Library/Application Support/' .. bundle .. '/QtsVFSCache'
local recovery = '/var/mobile/Library/XXTCloudCleanupRecovery/honor-' .. os.time()
local report_path = '/var/mobile/Media/1ferver/lua/scripts/honor-clean-trial.json'
-- Explicitly archive the previous trial report after inspecting recovery state.
-- An accidental second run must not replace the diagnostic or backup reference.
local previous = io.open(report_path, 'r')
if previous then previous:close(); return end
local function report(phase, reason)
    local f = assert(io.open(report_path, 'w'))
    f:write(assert(json.encode({phase=phase, error=reason, recovery=recovery})))
    f:close()
end
local function quote(s) return "'" .. s:gsub("'", "'\\''") .. "'" end
local function shell(command)
    local h = assert(io.popen(command .. ' 2>/dev/null && echo XXT_CLEAN_OK'))
    local result = h:read('*a'); h:close()
    assert(result:match('XXT_CLEAN_OK%s*$'), 'filesystem operation failed')
end
local function container()
    local p = assert(app.data_path(bundle))
    assert(p:match('^/private/var/mobile/Containers/Data/Application/[A-Fa-f0-9%-]+$'),
        'unexpected data container')
    return p
end
local function deny(p)
    local path = p .. '/Library/Preferences/com.iapguard.runtime.plist'
    plist.write(path, {enabled=true, allowedPriceQuotas={['0.29']=0}})
    local value=plist.read(path)
    assert(value and value.enabled == true and value.allowedPriceQuotas['0.29']==0,
        'deny policy readback failed')
end
local ok, err = pcall(function()
    report('stopping')
    local status = http.post('http://127.0.0.1:' .. tostring(sys.port()) .. '/api/webrtc/stop', 5)
    assert(status and status >= 200 and status < 300, 'remote stop failed')
    app.close(bundle)
    for _=1,50 do
        if not app.is_running(bundle) then break end
        sys.msleep(100)
    end
    assert(not app.is_running(bundle), 'game still running')
    local p=container()
    deny(p)
    -- RootHide shell paths need /rootfs; Lua filesystem APIs do not.
    local root='/rootfs' .. p
    local backup='/rootfs' .. recovery
    shell('umask 077; mkdir -p ' .. quote(backup))
    report('backing_up')
    shell('tar -cpf ' .. quote(backup .. '/data-without-resource.tar') ..
        ' --exclude=' .. quote('./' .. resource) .. ' -C ' .. quote(root) .. ' .')
    shell('tar -tf ' .. quote(backup .. '/data-without-resource.tar') .. ' >/dev/null')
    assert(os.rename(p .. '/' .. resource, recovery .. '/QtsVFSCache'))
    report('clearing')
    local called, success=pcall(clear.app_data, bundle)
    -- Always try to restore the saved resource, even if the API reported failure.
    p=container()
    shell('mkdir -p ' .. quote('/rootfs' .. p .. '/Library/Application Support/' .. bundle))
    assert(os.rename(recovery .. '/QtsVFSCache', p .. '/' .. resource), 'resource restore failed')
    shell('mkdir -p ' .. quote('/rootfs' .. p .. '/Library/Preferences'))
    shell('chown mobile:mobile ' .. quote('/rootfs' .. p .. '/Library/Application Support') ..
        ' ' .. quote('/rootfs' .. p .. '/Library/Application Support/' .. bundle) ..
        ' ' .. quote('/rootfs' .. p .. '/Library/Preferences'))
    deny(p)
    assert(called and success == true, 'clear.app_data did not confirm success')
    -- This status MUST NOT release a device: keychain/login UI remain unverified.
    report('awaiting_login_verification')
end)
if not ok then pcall(report, 'failed', tostring(err)) end
