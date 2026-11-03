function add(a, b) {
  return a + b
}

function sign(n) {
  if (n < 0) {
    return -1
  }
  return 1
}

module.exports = { add, sign }
