import { describe, expect, it, test } from 'vitest'
import { add, sign } from '../src/math'

test('passes', () => {
  expect(add(1, 2)).toBe(3)
})

test('fails', () => {
  console.log('about to add')
  expect(add(2, 2)).toBe(5)
})

test('equals an object', () => {
  expect({ total: add(1, 1) }).toEqual({ total: 3 })
})

test('throws', () => {
  throw new Error('boom')
})

test('times out', async () => {
  await new Promise((resolve) => setTimeout(resolve, 2000))
}, 100)

test.skip('skipped', () => {})

test.todo('later')

describe('outer', () => {
  describe('inner', () => {
    it('nested pass', () => {
      expect(sign(1)).toBe(1)
    })
    it('nested fail', () => {
      expect(add(1, 1)).toBe(3)
    })
  })
})

describe.concurrent('parallel', () => {
  it('a', async () => {})
  it('b', async () => {})
})

test.each([
  [1, 1, 2],
  [2, 2, 5],
])('add(%i, %i) -> %i', (a, b, want) => {
  expect(add(a, b)).toBe(want)
})

test.each([
  { a: 1, b: 2, want: 3 },
  { a: 0, b: 0, want: 1 },
])('object $a + $b', ({ a, b, want }) => {
  expect(add(a, b)).toBe(want)
})

describe.each([['x'], ['y']])('suite %s', (name) => {
  it('works', () => {
    expect(name.length).toBe(1)
  })
})

it.each(['hi', 'two words'])('word %s', (word) => {
  expect(word).not.toBe('two words')
})
