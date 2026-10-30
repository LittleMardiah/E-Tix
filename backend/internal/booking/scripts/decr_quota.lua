-- decr_quota.lua
-- KEYS[1]: quota:{event_id}:{zone_name}
-- ARGV[1]: quantity
local key = KEYS[1]
local qty = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', key) or "-1")

if current == -1 then
    return -2 -- Key not found (Quota not initialized)
end

if current < qty then
    return -1 -- Not enough quota
end

return redis.call('DECRBY', key, qty)
