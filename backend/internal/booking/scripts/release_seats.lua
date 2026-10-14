local key = KEYS[1]
local seats = cjson.decode(ARGV[1])
local booking_id = ARGV[2]
local released = 0

for i, pos in ipairs(seats) do
  local bit = redis.call('GETBIT', key, pos - 1)
  if bit == 1 then
    redis.call('SETBIT', key, pos - 1, 0)
    released = released + 1
  end
end

redis.call('DEL', 'booking:'..booking_id)
return cjson.encode({status='SUCCESS', released=released})
