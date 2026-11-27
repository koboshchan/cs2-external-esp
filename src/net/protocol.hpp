#pragma once

// Remote ESP wire protocol, version 1.
//
// This header is deliberately dependency-free (no Windows, no imgui) so it can
// be compiled and tested on any platform, and mirrored by the TypeScript relay
// and the Go viewer.
//
// Layout is little-endian and packed by hand (no struct padding), so the
// serialized bytes are identical across compilers and platforms.

#include <cmath>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace net
{
	constexpr uint32_t kMagic = 0x52325343; // 'CS2R' little-endian
	constexpr uint16_t kProtoVersion = 1;

	constexpr size_t kMaxFrameBytes = 64 * 1024;
	constexpr size_t kMinFrameBytes = 12;

	constexpr int kMaxPlayers = 64;
	constexpr int kMaxBones = 32;
	constexpr int kMaxNameLen = 32;
	constexpr int kMaxMapLen = 32;

	// Frame flags
	enum FrameFlags : uint16_t
	{
		kFrameHostAlive = 1 << 0,
		kFrameHostSpectating = 1 << 1,
	};

	// Player flags
	enum PlayerFlags : uint32_t
	{
		kPlayerAlive = 1 << 0,
		kPlayerScoped = 1 << 1,
		kPlayerFlashed = 1 << 2,
		kPlayerDefusing = 1 << 3,
		kPlayerSpotted = 1 << 4,
		kPlayerHasC4 = 1 << 5,
		kPlayerLocal = 1 << 6,
		kPlayerBot = 1 << 7,
		kPlayerReloading = 1 << 8,
	};

	// How the game turns a horizontal FOV into a projection.
	// 0 = Source "Hor+": fov is horizontal at 4:3, vertical is fixed.
	// 1 = fov is horizontal on the current aspect ratio.
	enum FovMode : uint8_t
	{
		kFovSourceHorPlus = 0,
		kFovHorizontal = 1,
	};

	inline float DegToRad(float deg) { return deg * 0.017453292519943295f; }

	struct Vec3
	{
		float x = 0.f, y = 0.f, z = 0.f;
	};

	struct Vec2
	{
		float x = 0.f, y = 0.f;
	};

	struct Camera
	{
		int8_t index = -1;        // player the camera belongs to
		uint8_t observer_mode = 0; // 0 alive, 2 first, 3 third, 4 free
		uint8_t team = 0;
		uint8_t fov_mode = kFovSourceHorPlus;

		Vec3 eye_pos{};
		Vec2 eye_angles{}; // pitch, yaw in degrees
		float fov = 90.f;

		// Host's own world-to-clip matrix, rows 0..3. Used to calibrate the
		// viewer's matrix construction; not required for rendering.
		float view_matrix[16]{};

		int8_t host_index = -1;
		Vec3 host_eye_pos{};
		Vec2 host_eye_angles{};
		float host_fov = 90.f;
	};

	struct Player
	{
		int8_t index = -1;
		uint8_t team = 0;
		uint16_t health = 0;
		uint16_t armor = 0;
		int32_t money = 0;
		uint32_t flags = 0;
		uint64_t steam_id = 0;
		std::string name;
		uint16_t weapon_id = 0;
		uint16_t ammo = 0;

		Vec3 pos{};
		Vec3 vel{};
		Vec2 eye_angles{};

		uint32_t bone_mask = 0;
		std::vector<Vec3> bones; // size must equal popcount(bone_mask)
	};

	struct Bomb
	{
		Vec3 pos{};
		float time_left = 0.f;
		bool is_planted = false;
		int8_t site = -1;
	};

	struct Frame
	{
		uint16_t flags = 0;
		uint32_t seq = 0;
		uint64_t host_time_ms = 0;
		float match_time = 0.f;
		std::string map_name;

		Camera camera;
		std::vector<Player> players;
		Bomb bomb;
	};

	class Writer
	{
	public:
		std::vector<uint8_t> data;

		void u8(uint8_t v) { data.push_back(v); }
		void u16(uint16_t v) { u8(uint8_t(v & 0xff)); u8(uint8_t((v >> 8) & 0xff)); }
		void u32(uint32_t v) { u16(uint16_t(v & 0xffff)); u16(uint16_t((v >> 16) & 0xffff)); }
		void u64(uint64_t v) { u32(uint32_t(v & 0xffffffff)); u32(uint32_t((v >> 32) & 0xffffffff)); }
		void i8(int8_t v) { u8(uint8_t(v)); }
		void i32(int32_t v) { u32(uint32_t(v)); }
		void f32(float v)
		{
			uint32_t bits = 0;
			std::memcpy(&bits, &v, sizeof(bits));
			u32(bits);
		}
		void bytes(const void* p, size_t n)
		{
			const auto* b = static_cast<const uint8_t*>(p);
			data.insert(data.end(), b, b + n);
		}
		void str8(const std::string& s)
		{
			const size_t n = s.size() > 255 ? 255 : s.size();
			u8(uint8_t(n));
			bytes(s.data(), n);
		}
		void vec3(const Vec3& v) { f32(v.x); f32(v.y); f32(v.z); }
		void vec2(const Vec2& v) { f32(v.x); f32(v.y); }
	};

	class Reader
	{
	public:
		Reader(const uint8_t* data, size_t size) : data_(data), size_(size) {}

		bool ok() const { return ok_; }
		bool done() const { return ok_ && pos_ == size_; }
		size_t remaining() const { return ok_ ? size_ - pos_ : 0; }

		uint8_t u8()
		{
			if (!need(1)) return 0;
			return data_[pos_++];
		}
		uint16_t u16()
		{
			const uint16_t lo = u8();
			const uint16_t hi = u8();
			return uint16_t(lo | (hi << 8));
		}
		uint32_t u32()
		{
			const uint32_t lo = u16();
			const uint32_t hi = u16();
			return lo | (hi << 16);
		}
		uint64_t u64()
		{
			const uint64_t lo = u32();
			const uint64_t hi = u32();
			return lo | (hi << 32);
		}
		int8_t i8() { return int8_t(u8()); }
		int32_t i32() { return int32_t(u32()); }
		float f32()
		{
			const uint32_t bits = u32();
			float v = 0.f;
			std::memcpy(&v, &bits, sizeof(v));
			return v;
		}
		bool bytes(void* out, size_t n)
		{
			if (!need(n)) return false;
			std::memcpy(out, data_ + pos_, n);
			pos_ += n;
			return true;
		}
		std::string str8()
		{
			const uint8_t n = u8();
			if (!need(n)) return {};
			std::string s(reinterpret_cast<const char*>(data_ + pos_), n);
			pos_ += n;
			return s;
		}
		Vec3 vec3() { Vec3 v; v.x = f32(); v.y = f32(); v.z = f32(); return v; }
		Vec2 vec2() { Vec2 v; v.x = f32(); v.y = f32(); return v; }

	private:
		bool need(size_t n)
		{
			if (!ok_ || pos_ + n > size_)
			{
				ok_ = false;
				return false;
			}
			return true;
		}

		const uint8_t* data_ = nullptr;
		size_t size_ = 0;
		size_t pos_ = 0;
		bool ok_ = true;
	};

	inline int Popcount(uint32_t v)
	{
		int n = 0;
		while (v) { v &= v - 1; n++; }
		return n;
	}

	inline bool IsFinite(float v) { return std::isfinite(v); }

	inline bool IsFinite(const Vec3& v) { return IsFinite(v.x) && IsFinite(v.y) && IsFinite(v.z); }
	inline bool IsFinite(const Vec2& v) { return IsFinite(v.x) && IsFinite(v.y); }

	// Validates a frame before it is serialized (host) or after it is parsed
	// (viewer). A viewer must treat every field as hostile input.
	inline bool Validate(const Frame& f, std::string* error = nullptr)
	{
		auto fail = [&](const char* msg)
		{
			if (error) *error = msg;
			return false;
		};

		if (f.map_name.size() > size_t(kMaxMapLen)) return fail("map name too long");
		if (f.players.size() > size_t(kMaxPlayers)) return fail("too many players");

		if (!IsFinite(f.camera.eye_pos) || !IsFinite(f.camera.eye_angles)) return fail("camera not finite");
		if (!IsFinite(f.camera.fov) || f.camera.fov <= 0.f || f.camera.fov > 180.f) return fail("camera fov out of range");
		if (f.camera.fov_mode > kFovHorizontal) return fail("bad fov mode");

		for (const auto& p : f.players)
		{
			if (p.name.size() > size_t(kMaxNameLen)) return fail("player name too long");
			if (!IsFinite(p.pos) || !IsFinite(p.vel) || !IsFinite(p.eye_angles)) return fail("player not finite");
			if (Popcount(p.bone_mask) > kMaxBones) return fail("too many bones");
			if (p.bones.size() != size_t(Popcount(p.bone_mask))) return fail("bone count does not match mask");
			for (const auto& b : p.bones)
				if (!IsFinite(b)) return fail("bone not finite");
		}

		if (!IsFinite(f.bomb.pos) || !IsFinite(f.bomb.time_left)) return fail("bomb not finite");
		return true;
	}

	inline std::vector<uint8_t> Serialize(const Frame& f)
	{
		Writer w;
		w.u32(kMagic);
		w.u16(kProtoVersion);
		w.u16(f.flags);
		w.u32(f.seq);
		w.u64(f.host_time_ms);
		w.f32(f.match_time);
		w.str8(f.map_name);
		w.u8(uint8_t(f.players.size()));

		const auto& c = f.camera;
		w.i8(c.index);
		w.u8(c.observer_mode);
		w.u8(c.team);
		w.u8(c.fov_mode);
		w.vec3(c.eye_pos);
		w.vec2(c.eye_angles);
		w.f32(c.fov);
		for (int i = 0; i < 16; i++) w.f32(c.view_matrix[i]);
		w.i8(c.host_index);
		w.vec3(c.host_eye_pos);
		w.vec2(c.host_eye_angles);
		w.f32(c.host_fov);

		for (const auto& p : f.players)
		{
			w.i8(p.index);
			w.u8(p.team);
			w.u16(p.health);
			w.u16(p.armor);
			w.i32(p.money);
			w.u32(p.flags);
			w.u64(p.steam_id);
			w.str8(p.name);
			w.u16(p.weapon_id);
			w.u16(p.ammo);
			w.vec3(p.pos);
			w.vec3(p.vel);
			w.vec2(p.eye_angles);
			w.u32(p.bone_mask);
			for (const auto& b : p.bones) w.vec3(b);
		}

		w.vec3(f.bomb.pos);
		w.f32(f.bomb.time_left);
		w.u8(f.bomb.is_planted ? 1 : 0);
		w.i8(f.bomb.site);

		return w.data;
	}

	inline bool Deserialize(const uint8_t* data, size_t size, Frame& out, std::string* error = nullptr)
	{
		auto fail = [&](const char* msg)
		{
			if (error) *error = msg;
			return false;
		};

		if (!data) return fail("null data");
		if (size < kMinFrameBytes) return fail("frame too small");
		if (size > kMaxFrameBytes) return fail("frame too large");

		Reader r(data, size);

		if (r.u32() != kMagic) return fail("bad magic");
		if (r.u16() != kProtoVersion) return fail("unsupported protocol version");

		Frame f;
		f.flags = r.u16();
		f.seq = r.u32();
		f.host_time_ms = r.u64();
		f.match_time = r.f32();
		f.map_name = r.str8();
		const uint8_t player_count = r.u8();

		if (!r.ok()) return fail("truncated header");
		if (player_count > kMaxPlayers) return fail("too many players");

		auto& c = f.camera;
		c.index = r.i8();
		c.observer_mode = r.u8();
		c.team = r.u8();
		c.fov_mode = r.u8();
		c.eye_pos = r.vec3();
		c.eye_angles = r.vec2();
		c.fov = r.f32();
		for (int i = 0; i < 16; i++) c.view_matrix[i] = r.f32();
		c.host_index = r.i8();
		c.host_eye_pos = r.vec3();
		c.host_eye_angles = r.vec2();
		c.host_fov = r.f32();

		if (!r.ok()) return fail("truncated camera block");

		f.players.reserve(player_count);
		for (int i = 0; i < player_count; i++)
		{
			Player p;
			p.index = r.i8();
			p.team = r.u8();
			p.health = r.u16();
			p.armor = r.u16();
			p.money = r.i32();
			p.flags = r.u32();
			p.steam_id = r.u64();
			p.name = r.str8();
			p.weapon_id = r.u16();
			p.ammo = r.u16();
			p.pos = r.vec3();
			p.vel = r.vec3();
			p.eye_angles = r.vec2();
			p.bone_mask = r.u32();

			if (!r.ok()) return fail("truncated player");

			const int bone_count = Popcount(p.bone_mask);
			if (bone_count > kMaxBones) return fail("too many bones");

			p.bones.reserve(bone_count);
			for (int b = 0; b < bone_count; b++)
			{
				p.bones.push_back(r.vec3());
				if (!r.ok()) return fail("truncated bones");
			}

			f.players.push_back(std::move(p));
		}

		f.bomb.pos = r.vec3();
		f.bomb.time_left = r.f32();
		f.bomb.is_planted = r.u8() != 0;
		f.bomb.site = r.i8();

		if (!r.ok()) return fail("truncated bomb block");
		if (!r.done()) return fail("trailing bytes");

		if (!Validate(f, error)) return false;

		out = std::move(f);
		return true;
	}

	inline bool Deserialize(const std::vector<uint8_t>& data, Frame& out, std::string* error = nullptr)
	{
		return Deserialize(data.data(), data.size(), out, error);
	}
}
