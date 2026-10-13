defmodule Pricing do
  @moduledoc "A tier's price, in pence."

  @discount 0.9

  def price_for(%{pence: pence}, qty) when qty >= 100 do
    round(pence * qty * @discount)
  end

  def price_for(%{pence: pence}, qty) when qty > 0, do: pence * qty
  def price_for(_tier, qty), do: {:error, "bad quantity #{qty}"}
end
