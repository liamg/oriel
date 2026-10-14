; Tests for testrun's vitest and jest adapters, for every language that inherits ecma. Ours, not nvim-treesitter's.
; describe("…", …), it.skip("…", …), test.concurrent.only("…", …)
((call_expression
  function: [
    (identifier) @test.fn
    (member_expression
      object: (identifier) @test.fn)
    (member_expression
      object: (member_expression
        object: (identifier) @test.fn))
  ]
  arguments: (arguments
    .
    [(string) (template_string)] @test.name)) @test.call
  (#any-of? @test.fn "describe" "suite" "context" "it" "test" "fdescribe" "fit" "xdescribe" "xit" "xtest"))

; test.each([...])("…", …), describe.skip.each([...])("…", …)
(call_expression
  function: (call_expression
    function: (member_expression
      object: [
        (identifier) @each.fn
        (member_expression
          object: (identifier) @each.fn)
      ]
      property: (property_identifier) @_each)
    arguments: (arguments
      .
      (array) @each.table))
  arguments: (arguments
    .
    [(string) (template_string)] @each.name)
  (#eq? @_each "each")
  (#any-of? @each.fn "describe" "suite" "context" "it" "test" "fdescribe" "fit" "xdescribe" "xit" "xtest")) @each.call

(array (_) @each.row)

(pair
  key: [(property_identifier) (string)] @each.key
  value: (_) @each.value)
