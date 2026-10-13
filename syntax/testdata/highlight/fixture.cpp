#include <string>
#include <vector>

namespace pricing {

template <typename T>
class Tier {
public:
	explicit Tier(std::string name) : name_(std::move(name)) {}
	T price(T base) const { return base * 2; }

private:
	std::string name_;
};

}  // namespace pricing

int main() {
	auto tiers = std::vector<pricing::Tier<int>>{pricing::Tier<int>("gold")};
	return tiers.front().price(21) == 42 ? 0 : 1;
}
