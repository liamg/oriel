# frozen_string_literal: true

module Pricing
  class Tier
    attr_reader :name

    def initialize(name, rate: 1.5)
      @name = name
      @rate = rate
    end

    def price(base)
      (base * @rate).round(2)
    end
  end
end

puts Pricing::Tier.new("gold").price(10) if __FILE__ == $PROGRAM_NAME
