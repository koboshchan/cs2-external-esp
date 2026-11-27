#include "net/publisher.hpp"

#include "net/camera.hpp"
#include "net/protocol.hpp"
#include "net/ws_client.hpp"

#include "common.hpp"
#include "core/engine/cache/Cache.hpp"
#include "core/offsets/Offsets.hpp"
#include "config/Current.hpp"

#include <windows.h>
#include <timeapi.h>

#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <random>

#pragma comment(lib, "winmm.lib")

namespace net
{
	namespace
	{
		WsClient g_client;

		uint64_t NowMs()
		{
			using namespace std::chrono;
			return duration_cast<milliseconds>(steady_clock::now().time_since_epoch()).count();
		}

		std::string JsonEscape(const std::string& in)
		{
			std::string out;
			out.reserve(in.size() + 8);
			for (char c : in)
			{
				if (c == '"' || c == '\\')
				{
					out.push_back('\\');
					out.push_back(c);
				}
				else if (static_cast<unsigned char>(c) < 0x20)
				{
					char buf[8];
					std::snprintf(buf, sizeof(buf), "\\u%04x", static_cast<unsigned char>(c));
					out += buf;
				}
				else
				{
					out.push_back(c);
				}
			}
			return out;
		}

		std::string BuildHello(const std::string& room, const std::string& token)
		{
			return "{\"t\":\"hello\",\"role\":\"host\",\"room\":\"" + JsonEscape(room) +
				"\",\"token\":\"" + JsonEscape(token) + "\",\"proto\":" + std::to_string(kProtoVersion) + "}";
		}

		// Fixed-size char array that may not be null terminated.
		std::string ReadCString(const char* data, size_t max_len)
		{
			size_t len = 0;
			while (len < max_len && data[len] != '\0') len++;
			return std::string(data, len);
		}

		const ::Player* FindCameraPlayer(const Snapshot& s)
		{
			const ::Player* local = nullptr;
			for (const auto& p : s.players)
			{
				if (p.localplayer)
				{
					local = &p;
					break;
				}
			}

			if (!local)
				return nullptr;

			// While dead or spectating, follow the observer target so the
			// viewer keeps a valid camera.
			const auto mode = local->observer_services.mode;
			const int target = local->observer_services.target;
			const bool spectating = mode == ObserverMode::First || mode == ObserverMode::Third || mode == ObserverMode::Free;

			if (spectating && target != 0)
			{
				for (const auto& p : s.players)
				{
					if (p.pawn_controller_addr == target)
						return &p;
				}
			}

			return local;
		}

		Vec3 EyePosition(const ::Player& p)
		{
			return { p.pos.x + p.view_offset.x, p.pos.y + p.view_offset.y, p.pos.z + p.view_offset.z };
		}

		uint32_t PlayerFlags(const ::Player& p)
		{
			uint32_t f = 0;
			if (p.alive) f |= kPlayerAlive;
			if (p.scoped) f |= kPlayerScoped;
			if (p.flashed) f |= kPlayerFlashed;
			if (p.defusing) f |= kPlayerDefusing;
			if (p.spotted) f |= kPlayerSpotted;
			if (p.has_c4) f |= kPlayerHasC4;
			if (p.localplayer) f |= kPlayerLocal;
			if (p.bot) f |= kPlayerBot;
			if (p.is_reloading) f |= kPlayerReloading;
			return f;
		}

		Frame BuildFrame(const Snapshot& s, uint32_t seq)
		{
			Frame f;
			f.seq = seq;
			f.host_time_ms = NowMs();
			f.match_time = static_cast<float>(s.globals.current_time);
			f.map_name = ReadCString(s.globals.map_name, sizeof(s.globals.map_name));
			f.flags = kFrameHostAlive;

			const ::Player* cam = FindCameraPlayer(s);
			if (cam)
			{
				f.camera.index = cam->index;
				f.camera.team = static_cast<uint8_t>(cam->team);
				f.camera.eye_pos = EyePosition(*cam);
				f.camera.eye_angles = { cam->eye_angles.x, cam->eye_angles.y };
				f.camera.fov = cam->fov > 1.f && cam->fov < 180.f ? cam->fov : 90.f;

				const auto mode = cam->observer_services.mode;
				f.camera.observer_mode = static_cast<uint8_t>(mode);
				if (mode != ObserverMode::Alive && mode != ObserverMode::Unknown)
					f.flags |= kFrameHostSpectating;
			}

			// Host camera + real matrix, so the viewer can calibrate its own
			// matrix construction against ground truth.
			if (s.local.index >= 0)
			{
				f.camera.host_index = s.local.index;
				f.camera.host_eye_pos = EyePosition(s.local);
				f.camera.host_eye_angles = { s.local.eye_angles.x, s.local.eye_angles.y };
				f.camera.host_fov = s.local.fov > 1.f && s.local.fov < 180.f ? s.local.fov : 90.f;
			}

			std::memcpy(f.camera.view_matrix, &s.game.view_matrix.matrix[0][0], sizeof(f.camera.view_matrix));

			// The game's own matrix tells us how FOV maps to a projection.
			const float xs = MatrixXScale(f.camera.view_matrix);
			const float ys = MatrixYScale(f.camera.view_matrix);
			if (xs > 0.f && ys > 0.f && f.camera.host_fov > 1.f)
			{
				const float aspect = ys / xs;
				f.camera.fov_mode = DetectFovMode(f.camera.host_fov, aspect, f.camera.view_matrix).mode;
			}

			f.players.reserve(s.players.size());
			for (const auto& p : s.players)
			{
				net::Player np;
				np.index = p.index;
				np.team = static_cast<uint8_t>(p.team);
				np.health = static_cast<uint16_t>(p.health < 0 ? 0 : p.health);
				np.armor = static_cast<uint16_t>(p.armor < 0 ? 0 : p.armor);
				np.money = p.money;
				np.flags = PlayerFlags(p);
				np.steam_id = cfg::remote::publish_steam_ids ? p.steam_id : 0;
				np.name = cfg::remote::publish_names ? ReadCString(p.name, sizeof(p.name)) : std::string{};
				np.weapon_id = static_cast<uint16_t>(p.weapon.item_index < 0 ? 0 : p.weapon.item_index);
				np.ammo = static_cast<uint16_t>(p.ammo < 0 ? 0 : p.ammo);
				np.pos = { p.pos.x, p.pos.y, p.pos.z };
				np.vel = { p.vel.x, p.vel.y, p.vel.z };
				np.eye_angles = { p.eye_angles.x, p.eye_angles.y };
				np.eye_pos = EyePosition(p);
				np.fov = p.fov > 1.f && p.fov < 180.f ? p.fov : 90.f;

				// Bones 0..23 are the ones the skeleton/box renderer uses.
				for (int b = 0; b < 24 && b < static_cast<int>(p.bone_list.size()); b++)
				{
					const Vec3_t& bp = p.bone_list[b].pos;
					if (bp.x == 0.f && bp.y == 0.f && bp.z == 0.f)
						continue;
					np.bone_mask |= (1u << b);
					np.bones.push_back({ bp.x, bp.y, bp.z });
				}

				f.players.push_back(std::move(np));
			}

			f.bomb.pos = { s.bomb.pos.x, s.bomb.pos.y, s.bomb.pos.z };
			f.bomb.time_left = s.bomb.time_left;
			f.bomb.is_planted = s.bomb.is_planted;
			f.bomb.site = static_cast<int8_t>(s.bomb.site);

			return f;
		}

		Frame BuildSyntheticFrame(uint32_t seq, int tick)
		{
			Frame f;
			f.seq = seq;
			f.host_time_ms = NowMs();
			f.match_time = static_cast<float>(tick);
			f.map_name = "de_selftest";
			f.flags = kFrameHostAlive;

			const float t = static_cast<float>(tick) * 0.05f;

			f.camera.index = 0;
			f.camera.team = 2;
			f.camera.eye_pos = { 0.f, 0.f, 64.f };
			// Gentle sweep so the fan of players stays in view.
			f.camera.eye_angles = { 0.f, std::sin(t * 0.5f) * 15.f };
			f.camera.fov = 90.f;
			f.camera.fov_mode = kFovSourceHorPlus;
			BuildViewMatrix(f.camera.eye_pos, f.camera.eye_angles, f.camera.fov, 16.f / 9.f, f.camera.fov_mode, f.camera.view_matrix);
			f.camera.host_index = 0;
			f.camera.host_eye_pos = f.camera.eye_pos;
			f.camera.host_eye_angles = f.camera.eye_angles;
			f.camera.host_fov = 90.f;

			for (int i = 0; i < 10; i++)
			{
				Player p;
				p.index = static_cast<int8_t>(i);
				p.team = static_cast<uint8_t>(i % 2 == 0 ? 2 : 3);
				p.health = static_cast<uint16_t>(100 - i);
				p.armor = 100;
				p.money = 800;
				p.flags = kPlayerAlive | (i == 0 ? kPlayerLocal : 0);
				p.steam_id = 76561190000000000ull + static_cast<uint64_t>(i);
				p.name = "selftest" + std::to_string(i);
				p.weapon_id = 7;
				p.ammo = 30;
				// Fan in front of the camera: 10 players, ~36 degrees wide.
				p.pos = { 500.f + float(i % 3) * 120.f, float(i - 4) * 90.f, 0.f };
				p.vel = { 0.f, 0.f, 0.f };
				p.eye_angles = { 0.f, 0.f };
				p.eye_pos = { p.pos.x, p.pos.y, p.pos.z + 64.f };
				p.fov = 90.f;

				for (int b = 1; b <= 7; b++)
				{
					p.bone_mask |= (1u << b);
					p.bones.push_back({ p.pos.x, p.pos.y, p.pos.z + b * 10.f });
				}

				f.players.push_back(std::move(p));
			}

			f.bomb.pos = { 100.f, 100.f, 0.f };
			f.bomb.time_left = 40.f;
			f.bomb.is_planted = true;
			f.bomb.site = 0;

			return f;
		}
	}

	Publisher& Publisher::Get()
	{
		static Publisher instance;
		return instance;
	}

	Publisher::~Publisher()
	{
		Stop();
	}

	bool Publisher::Start()
	{
		Publisher& self = Get();
		if (self.running_)
			return true;

		self.url_ = cfg::remote::url;
		self.room_ = cfg::remote::room;
		self.token_ = cfg::remote::token;
		self.rate_hz_ = cfg::remote::rate > 0 ? cfg::remote::rate : 30;

		if (self.url_.empty() || self.room_.empty() || self.token_.empty())
		{
			LOGF(WARNING, "[net] publisher not started: url, room or host token is empty");
			return false;
		}

		// 1 ms timer resolution: without it sleep_for(33ms) rounds up to
		// ~46 ms on Windows and the frame rate drops to ~22 Hz.
		timeBeginPeriod(1);

		self.running_ = true;
		self.thread_ = std::thread(&Publisher::Thread, &self);

		LOGF(INFO, "[net] publishing to {} (room {}, {} Hz)", self.url_, self.room_, self.rate_hz_);
		return true;
	}

	void Publisher::Stop()
	{
		Publisher& self = Get();
		if (!self.running_)
			return;

		self.running_ = false;
		if (self.thread_.joinable())
			self.thread_.join();

		g_client.Close();
		self.connected_ = false;
		timeEndPeriod(1);
	}

	bool Publisher::IsRunning()
	{
		return Get().running_;
	}

	PublisherStats Publisher::GetStats()
	{
		Publisher& self = Get();
		PublisherStats stats;
		stats.running = self.running_;
		stats.connected = self.connected_;
		stats.frames_sent = self.frames_sent_;
		stats.bytes_sent = self.bytes_sent_;
		stats.dropped = self.dropped_;
		stats.last_error = self.last_error_;
		{
			std::lock_guard<std::mutex> lock(self.message_mutex_);
			stats.last_message = self.last_message_;
		}
		return stats;
	}

	bool Publisher::ConnectOnce()
	{
		Publisher& self = Get();
		std::string error;

		// Must be installed before Connect: the receive thread starts there.
		g_client.SetOnMessage([](bool binary, const uint8_t* data, size_t size)
		{
			if (binary)
				return;

			const std::string text(reinterpret_cast<const char*>(data), size);
			LOGF(INFO, "[net] relay: {}", text);

			if (text.find("\"error\"") != std::string::npos)
			{
				Publisher& p = Get();
				p.last_error_++;
				std::lock_guard<std::mutex> lock(p.message_mutex_);
				p.last_message_ = text;
			}
		});

		if (!g_client.Connect(self.url_, &error))
		{
			self.last_error_++;
			{
				std::lock_guard<std::mutex> lock(self.message_mutex_);
				self.last_message_ = error;
			}
			LOGF(WARNING, "[net] connect failed: {}", error);
			return false;
		}

		if (!g_client.SendText(BuildHello(self.room_, self.token_)))
		{
			self.last_error_++;
			g_client.Close();
			return false;
		}

		// Give the relay a moment to accept or reject us.
		std::this_thread::sleep_for(std::chrono::milliseconds(150));
		self.connected_ = g_client.IsConnected();

		if (self.connected_)
			LOGF(INFO, "[net] connected to relay, room {}", self.room_);

		return self.connected_;
	}

	bool Publisher::PublishFrame()
	{
		Publisher& self = Get();

		const Snapshot snapshot = Cache::CopySnapshot();
		const Frame frame = BuildFrame(snapshot, self.seq_++);

		std::string error;
		if (!Validate(frame, &error))
		{
			self.dropped_++;
			LOGF(WARNING, "[net] refusing to publish invalid frame: {}", error);
			return true;
		}

		const std::vector<uint8_t> bytes = Serialize(frame);
		if (bytes.size() > kMaxFrameBytes)
		{
			self.dropped_++;
			return true;
		}

		if (!g_client.SendBinary(bytes.data(), bytes.size()))
		{
			self.connected_ = false;
			self.last_error_++;
			LOGF(WARNING, "[net] send failed, reconnecting");
			g_client.Close();
			return false;
		}

		self.frames_sent_++;
		self.bytes_sent_ += bytes.size();
		return true;
	}

	void Publisher::Thread()
	{
		Publisher& self = Get();

		const auto tick = std::chrono::milliseconds(1000 / (self.rate_hz_ > 0 ? self.rate_hz_ : 30));
		int backoff_ms = 1000;

		auto last_poll = std::chrono::steady_clock::now();

		while (self.running_)
		{
			if (!g_client.IsConnected())
			{
				if (!self.ConnectOnce())
				{
					// Exponential backoff, capped at 30s, but wake up early on stop.
					for (int slept = 0; slept < backoff_ms && self.running_; slept += 250)
						std::this_thread::sleep_for(std::chrono::milliseconds(250));
					backoff_ms = (backoff_ms * 2 > 30000) ? 30000 : backoff_ms * 2;
					continue;
				}
				backoff_ms = 1000;
				last_poll = std::chrono::steady_clock::now();
			}

			const auto next = std::chrono::steady_clock::now() + tick;
			self.PublishFrame();

			// Control messages are rare; a receive attempt can block for the
			// session receive timeout, so do not run it every frame.
			const auto now = std::chrono::steady_clock::now();
			if (now - last_poll > std::chrono::milliseconds(500))
			{
				g_client.Poll(50);
				last_poll = now;
			}

			if (self.running_)
				std::this_thread::sleep_until(next);
		}

		g_client.Close();
		self.connected_ = false;
		LOGF(INFO, "[net] publisher stopped");
	}

	int Publisher::RunSelfTest(const std::string& url, const std::string& room, const std::string& token, int seconds)
	{
		LOGF(INFO, "[net] self-test: {} room {} for {}s", url, room, seconds);
		std::fprintf(stderr, "[net] self-test: url=%s room=%s seconds=%d\n", url.c_str(), room.c_str(), seconds);
		std::fprintf(stderr, "[net] libcurl runtime: %s\n", WsClient::RuntimeInfo().c_str());

		std::string connect_error;

		timeBeginPeriod(1);

		g_client.SetOnMessage([](bool binary, const uint8_t* data, size_t size)
		{
			if (binary)
			{
				std::fprintf(stderr, "[net] self-test received %zu bytes back\n", size);
				LOGF(INFO, "[net] self-test received {} bytes back", size);
				return;
			}

			const std::string text(reinterpret_cast<const char*>(data), size);
			std::fprintf(stderr, "[net] self-test relay: %s\n", text.c_str());
			LOGF(INFO, "[net] self-test relay: {}", text);
		});

		if (!g_client.Connect(url, &connect_error))
		{
			std::fprintf(stderr, "[net] self-test connect failed: %s\n", connect_error.c_str());
			LOGF(FATAL, "[net] self-test could not connect: {}", connect_error);
			return 2;
		}
		std::fprintf(stderr, "[net] self-test connected\n");

		if (!g_client.SendText(BuildHello(room, token)))
		{
			LOGF(FATAL, "[net] self-test hello failed");
			return 3;
		}

		g_client.Poll(500);
		if (!g_client.IsConnected())
		{
			LOGF(FATAL, "[net] self-test rejected by relay");
			return 4;
		}

		uint32_t seq = 0;
		uint64_t sent = 0;
		const auto end = std::chrono::steady_clock::now() + std::chrono::seconds(seconds);
		auto last_poll = std::chrono::steady_clock::now();

		while (std::chrono::steady_clock::now() < end)
		{
			const Frame frame = BuildSyntheticFrame(seq++, static_cast<int>(seq));

			std::string error;
			if (!Validate(frame, &error))
			{
				LOGF(WARNING, "[net] self-test invalid frame: {}", error);
				break;
			}

			const std::vector<uint8_t> bytes = Serialize(frame);
			if (!g_client.SendBinary(bytes.data(), bytes.size()))
			{
				std::fprintf(stderr, "[net] self-test send failed after %llu frames\n", static_cast<unsigned long long>(sent));
				LOGF(WARNING, "[net] self-test send failed after {} frames", sent);
				g_client.Close();
				return 5;
			}

			sent++;

			const auto now = std::chrono::steady_clock::now();
			if (now - last_poll > std::chrono::milliseconds(500))
			{
				g_client.Poll(50);
				last_poll = now;
			}

			std::this_thread::sleep_for(std::chrono::milliseconds(33));
		}

		g_client.Close();
		timeEndPeriod(1);
		std::fprintf(stderr, "[net] self-test sent %llu frames\n", static_cast<unsigned long long>(sent));
		LOGF(INFO, "[net] self-test sent {} frames", sent);
		return sent > 0 ? 0 : 6;
	}
}
