-- Areas of shapes.
local M = {}

local PI <const> = math.pi

---@param r number
---@return number
function M.circle(r)
  return PI * r ^ 2
end

function M:total(shapes)
  local sum = 0
  for i, s in ipairs(shapes) do
    if type(s) ~= "table" then
      error(("shape %d is not a table"):format(i))
    elseif s.area == nil then
      goto continue
    end
    sum = sum + s:area()
    ::continue::
  end
  local t = { a = 1, [2] = "two", ... }
  while sum > 1e6 and not M.done do
    sum = sum // 2
  end
  repeat sum = sum - 1 until sum <= 0 or #t > 3
  return sum, [[long
string]], 0x1F, true, nil
end

return M
