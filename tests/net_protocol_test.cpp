// Protocol round-trip, validation and robustness tests.
//
// Builds with any C++20 compiler, no Windows headers needed:
//   clang++ -std=c++20 -O1 -I src tests/net_protocol_test.cpp -o /tmp/net_protocol_test

#include "net/protocol.hpp"

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <random>
#include <string>
#include <vector>

namespace
{
	int g_failures = 0;

	void check(bool cond, const char* what)
	{
		if (!cond)
		{
			std::printf("FAIL: %s\n", what);
			g_failures++;
		}
	}

	bool nearly(float a, float b) { return std::fabs(a - b) < 1e-6f; }

	net::Frame MakeFrame()
	{
		net::Frame f;
		f.flags = net::kFrameHostAlive;
		f.seq = 1234;
		f.host_time_ms = 987654321ull;
		f.match_time = 42.5f;
		f.map_name = "de_dust2";

		f.camera.index = 3;
		f.camera.observer_mode = 0;
		f.camera.team = 2;
		f.camera.fov_mode = net::kFovSourceHorPlus;
		f.camera.eye_pos = { 100.f, -200.f, 64.f };
		f.camera.eye_angles = { -12.5f, 45.25f };
		f.camera.fov = 90.f;
		for (int i = 0; i < 16; i++) f.camera.view_matrix[i] = float(i) * 0.5f;
		f.camera.host_index = 3;
		f.camera.host_eye_pos = { 100.f, -200.f, 64.f };
		f.camera.host_eye_angles = { -12.5f, 45.25f };
		f.camera.host_fov = 90.f;

		for (int i = 0; i < 3; i++)
		{
			net::Player p;
			p.index = int8_t(i);
			p.team = uint8_t(2 + (i % 2));
			p.health = uint16_t(100 - i * 10);
			p.armor = uint16_t(50 + i);
			p.money = 800 - i * 100;
			p.flags = net::kPlayerAlive | (i == 1 ? net::kPlayerScoped : 0);
			p.steam_id = 76561190000000000ull + uint64_t(i);
			p.name = "player" + std::to_string(i);
			p.weapon_id = uint16_t(7 + i);
			p.ammo = uint16_t(30 - i);
			p.pos = { float(i) * 10.f, float(i) * -20.f, 0.5f };
			p.vel = { 1.f, 2.f, 3.f };
			p.eye_angles = { -1.f, 90.f };
			p.eye_pos = { p.pos.x, p.pos.y, p.pos.z + 64.f };
			p.fov = 90.f;

			// bones 1, 6, 7, 23
			const int ids[] = { 1, 6, 7, 23 };
			for (int id : ids)
			{
				p.bone_mask |= (1u << id);
				p.bones.push_back({ float(id), float(id) * 2.f, float(id) * 3.f });
			}

			f.players.push_back(p);
		}

		f.bomb.pos = { -1000.f, 2000.f, 0.f };
		f.bomb.time_left = 31.75f;
		f.bomb.is_planted = true;
		f.bomb.site = 0;

		return f;
	}

	bool SameFrame(const net::Frame& a, const net::Frame& b)
	{
		if (a.flags != b.flags || a.seq != b.seq || a.host_time_ms != b.host_time_ms) return false;
		if (!nearly(a.match_time, b.match_time) || a.map_name != b.map_name) return false;

		const auto& ca = a.camera;
		const auto& cb = b.camera;
		if (ca.index != cb.index || ca.observer_mode != cb.observer_mode || ca.team != cb.team) return false;
		if (ca.fov_mode != cb.fov_mode) return false;
		if (!nearly(ca.eye_pos.x, cb.eye_pos.x) || !nearly(ca.eye_pos.y, cb.eye_pos.y) || !nearly(ca.eye_pos.z, cb.eye_pos.z)) return false;
		if (!nearly(ca.eye_angles.x, cb.eye_angles.x) || !nearly(ca.eye_angles.y, cb.eye_angles.y)) return false;
		if (!nearly(ca.fov, cb.fov)) return false;
		for (int i = 0; i < 16; i++)
			if (!nearly(ca.view_matrix[i], cb.view_matrix[i])) return false;
		if (ca.host_index != cb.host_index) return false;
		if (!nearly(ca.host_fov, cb.host_fov)) return false;

		if (a.players.size() != b.players.size()) return false;
		for (size_t i = 0; i < a.players.size(); i++)
		{
			const auto& pa = a.players[i];
			const auto& pb = b.players[i];
			if (pa.index != pb.index || pa.team != pb.team) return false;
			if (pa.health != pb.health || pa.armor != pb.armor || pa.money != pb.money) return false;
			if (pa.flags != pb.flags || pa.steam_id != pb.steam_id) return false;
			if (pa.name != pb.name || pa.weapon_id != pb.weapon_id || pa.ammo != pb.ammo) return false;
			if (!nearly(pa.pos.x, pb.pos.x) || !nearly(pa.pos.y, pb.pos.y) || !nearly(pa.pos.z, pb.pos.z)) return false;
			if (!nearly(pa.vel.x, pb.vel.x) || !nearly(pa.vel.y, pb.vel.y) || !nearly(pa.vel.z, pb.vel.z)) return false;
			if (!nearly(pa.eye_angles.x, pb.eye_angles.x) || !nearly(pa.eye_angles.y, pb.eye_angles.y)) return false;
			if (!nearly(pa.eye_pos.x, pb.eye_pos.x) || !nearly(pa.eye_pos.y, pb.eye_pos.y) || !nearly(pa.eye_pos.z, pb.eye_pos.z)) return false;
			if (!nearly(pa.fov, pb.fov)) return false;
			if (pa.bone_mask != pb.bone_mask || pa.bones.size() != pb.bones.size()) return false;
			for (size_t b = 0; b < pa.bones.size(); b++)
				if (!nearly(pa.bones[b].x, pb.bones[b].x) || !nearly(pa.bones[b].y, pb.bones[b].y) || !nearly(pa.bones[b].z, pb.bones[b].z))
					return false;
		}

		if (!nearly(a.bomb.pos.x, b.bomb.pos.x) || !nearly(a.bomb.pos.y, b.bomb.pos.y) || !nearly(a.bomb.pos.z, b.bomb.pos.z)) return false;
		if (!nearly(a.bomb.time_left, b.bomb.time_left)) return false;
		if (a.bomb.is_planted != b.bomb.is_planted || a.bomb.site != b.bomb.site) return false;

		return true;
	}

	void DumpHex(const std::vector<uint8_t>& bytes)
	{
		for (size_t i = 0; i < bytes.size(); i++)
		{
			if (i % 16 == 0) std::printf("\n    \"");
			std::printf("%02x", bytes[i]);
			if (i % 16 == 15 || i + 1 == bytes.size()) std::printf("\"");
		}
		std::printf("\n");
	}
}

int main(int argc, char** argv)
{
	const net::Frame original = MakeFrame();

	const std::vector<uint8_t> bytes = net::Serialize(original);

	// Header sanity: magic, version, and a plausible size.
	check(bytes.size() >= net::kMinFrameBytes, "serialized size >= min");
	check(bytes.size() <= net::kMaxFrameBytes, "serialized size <= max");
	check(bytes[0] == 0x43 && bytes[1] == 0x53 && bytes[2] == 0x32 && bytes[3] == 0x52, "magic bytes are CS2R");

	if (argc > 1 && std::strcmp(argv[1], "--hex") == 0)
	{
		std::printf("{\n  \"size\": %zu,\n  \"hex\":", bytes.size());
		DumpHex(bytes);
		std::printf("}\n");
		return 0;
	}

	net::Frame parsed;
	std::string error;
	check(net::Deserialize(bytes.data(), bytes.size(), parsed, &error), ("deserialize: " + error).c_str());
	check(SameFrame(original, parsed), "round trip preserves every field");

	// Re-serializing must be byte identical.
	const std::vector<uint8_t> again = net::Serialize(parsed);
	check(again == bytes, "re-serialize is byte identical");

	// Every truncation must fail cleanly.
	for (size_t cut = 0; cut < bytes.size(); cut++)
	{
		net::Frame tmp;
		const bool ok = net::Deserialize(bytes.data(), cut, tmp, nullptr);
		check(!ok, "truncated frame rejected");
		if (ok) break;
	}

	// Trailing bytes are rejected.
	{
		std::vector<uint8_t> extended = bytes;
		extended.push_back(0);
		net::Frame tmp;
		check(!net::Deserialize(extended.data(), extended.size(), tmp, nullptr), "trailing bytes rejected");
	}

	// Bad magic / version.
	{
		std::vector<uint8_t> bad = bytes;
		bad[0] = 0;
		net::Frame tmp;
		check(!net::Deserialize(bad.data(), bad.size(), tmp, nullptr), "bad magic rejected");

		bad = bytes;
		bad[4] = 0x09; // unsupported version
		check(!net::Deserialize(bad.data(), bad.size(), tmp, nullptr), "bad version rejected");
	}

	// Mutation fuzz: must never crash, and anything accepted must validate.
	{
		std::mt19937 rng(1234);
		int accepted = 0;
		for (int iter = 0; iter < 20000; iter++)
		{
			std::vector<uint8_t> mutated = bytes;
			const int edits = 1 + int(rng() % 4);
			for (int e = 0; e < edits; e++)
				mutated[rng() % mutated.size()] = uint8_t(rng() & 0xff);

			net::Frame tmp;
			if (net::Deserialize(mutated.data(), mutated.size(), tmp, nullptr))
			{
				accepted++;
				check(net::Validate(tmp, nullptr), "accepted mutant is valid");
			}
		}
		std::printf("mutation fuzz: %d/%d accepted\n", accepted, 20000);
	}

	// Validation rejects out-of-range data that the wire format allows.
	{
		net::Frame bad = MakeFrame();
		bad.camera.fov = 0.f;
		check(!net::Validate(bad, nullptr), "fov 0 rejected");

		bad = MakeFrame();
		bad.players[0].bones.pop_back();
		check(!net::Validate(bad, nullptr), "bone/mask mismatch rejected");

		bad = MakeFrame();
		bad.players[0].pos.x = std::nanf("");
		check(!net::Validate(bad, nullptr), "NaN position rejected");

		bad = MakeFrame();
		bad.players[0].name = std::string(40, 'a');
		check(!net::Validate(bad, nullptr), "long name rejected");

		bad = MakeFrame();
		bad.camera.fov_mode = 9;
		check(!net::Validate(bad, nullptr), "bad fov mode rejected");
	}

	if (g_failures == 0)
		std::printf("protocol: all checks passed (%zu bytes/frame with 3 players)\n", bytes.size());

	return g_failures == 0 ? 0 : 1;
}
