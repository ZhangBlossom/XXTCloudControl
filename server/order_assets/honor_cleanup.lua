-- Honor data-container reset with retained downloaded resources. No keychain cleanup.
local lfs = require('lfs')
local bundle = 'com.levelinfinite.sgameGlobal'
local work = '/var/mobile/Library/XXTCloudCleanupRecovery/honor-production'
local marker = work .. '/state.json'
local saved = work .. '/QtsVFSCache'
local resource = 'Library/Application Support/' .. bundle .. '/QtsVFSCache'
local policy_relative = 'Library/Preferences/com.iapguard.runtime.plist'
local M = {}
local function quote(s) return "'" .. s:gsub("'", "'\\''") .. "'" end
local function shell(command)
    local h = assert(io.popen(command .. ' 2>/dev/null && echo XXT_CLEAN_OK'))
    local result = h:read('*a'); h:close()
    assert(result:match('XXT_CLEAN_OK%s*$'), 'cleanup filesystem operation failed')
end
local function container(bid)
    assert(bid == bundle, 'unsupported cleanup game')
    local p = assert(app.data_path(bundle), 'missing data container')
    assert(p:match('^/private/var/mobile/Containers/Data/Application/[A-Fa-f0-9%-]+$'), 'unexpected data container')
    return p
end
function M.policy_path(bid) return container(bid) .. '/' .. policy_relative end
local function read_marker()
    local f = io.open(marker, 'r')
    if not f then return nil end
    local text = f:read('*a'); f:close()
    return assert(json.decode(text), 'invalid cleanup marker')
end
local function state(phase, p, reason)
    local f = assert(io.open(marker .. '.tmp', 'w'))
    assert(f:write(assert(json.encode({phase=phase, container=p, error=reason}))))
    assert(f:close()); assert(os.rename(marker .. '.tmp', marker))
end
local function deny(p)
    local target = p .. '/' .. policy_relative
    plist.write(target, {enabled=true, allowedPriceQuotas={['0.29']=0}})
    local value = plist.read(target)
    assert(value and value.enabled == true and type(value.allowedPriceQuotas) == 'table' and
        value.allowedPriceQuotas['0.29'] == 0, 'cleanup deny policy unconfirmed')
end
-- Inspect names/types only. Never load user preferences, account identifiers or tokens.
local function reset_evidence(p, relative)
    for name in lfs.dir(p .. (relative == '' and '' or '/' .. relative)) do
        if name ~= '.' and name ~= '..' then
            local rel = relative == '' and name or relative .. '/' .. name
            local mode = lfs.symlinkattributes(p .. '/' .. rel, 'mode')
            if rel == resource then
                if mode ~= 'directory' then return false, rel end
            elseif rel == policy_relative or rel == '.com.apple.mobile_container_manager.metadata.plist' then
                if mode ~= 'file' then return false, rel end
            elseif mode == 'directory' then
                local clean, residual = reset_evidence(p, rel)
                if not clean then return false, residual end
            else
                return false, rel
            end
        end
    end
    return true
end
function M.verify(bid)
    local ok, result = pcall(function()
        local p = container(bid)
        if app.is_running(bundle) or lfs.symlinkattributes(saved) then return false end
        local value = read_marker()
        if not value or value.phase ~= 'clean' or value.container ~= p then return false end
        return reset_evidence(p, '')
    end)
    return ok and result == true
end
function M.clean(bid)
    -- Refuse ambiguous previous attempts before changing anything.
    local ready, p = pcall(function()
        local path = container(bid)
        assert(not app.is_running(bundle), 'game still running')
        local old = read_marker()
        assert(not old or old.phase == 'clean', 'previous cleanup requires recovery')
        assert(not lfs.symlinkattributes(saved), 'saved resources require recovery')
        return path
    end)
    if not ready then return false, tostring(p) end
    local ok, failure = pcall(function()
        shell('umask 077; mkdir -p ' .. quote('/rootfs' .. work))
        shell('chmod 700 ' .. quote('/rootfs' .. work))
        state('cleaning', p)
        local source = p .. '/' .. resource
        local mode = lfs.symlinkattributes(source, 'mode')
        assert(not mode or mode == 'directory', 'unexpected resource path')
        if mode then assert(os.rename(source, saved), 'resource staging failed') end
        local called, cleared = pcall(clear.app_data, bundle)
        p = container(bid)
        -- Even a failing/throwing cleanup API must not strand the downloaded resources.
        if mode then
            shell('mkdir -p ' .. quote('/rootfs' .. p .. '/Library/Application Support/' .. bundle))
            assert(os.rename(saved, p .. '/' .. resource), 'resource restore failed')
        end
        shell('mkdir -p ' .. quote('/rootfs' .. p .. '/Library/Preferences'))
        shell('chown mobile:mobile ' .. quote('/rootfs' .. p .. '/Library') ..
            ' ' .. quote('/rootfs' .. p .. '/Library/Preferences'))
        if mode then
            shell('chown mobile:mobile ' .. quote('/rootfs' .. p .. '/Library/Application Support') ..
                ' ' .. quote('/rootfs' .. p .. '/Library/Application Support/' .. bundle))
        end
        deny(p)
        assert(called and cleared == true, 'clear.app_data did not confirm success')
        assert(not app.is_running(bundle), 'game_reopened_during_cleanup')
        local clean, residual = reset_evidence(p, '')
        assert(clean, 'user files remain after cleanup: ' .. tostring(residual))
        assert(not app.is_running(bundle), 'game_reopened_during_cleanup')
        state('clean', p)
    end)
    if not ok then pcall(state, 'failed', p, tostring(failure)); return false, tostring(failure) end
    return M.verify(bid)
end
return M
