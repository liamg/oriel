; Tests for testrun's cargo adapter. Ours, not nvim-treesitter's.
; #[test], #[tokio::test], and other attributes before the function
((attribute_item
  (attribute
    [(identifier) @_test
     (scoped_identifier
       name: (identifier) @_test)]))
  .
  [(attribute_item) (line_comment) (block_comment)]*
  .
  (function_item
    name: (identifier) @test.name) @test.func
  (#eq? @_test "test"))

(mod_item
  name: (identifier) @suite.name
  body: (declaration_list)) @suite
