#pragma once

// Camera math shared by the host (calibration) and the viewer (projection).
//
// The matrix layout matches the game's `view_matrix_t` as consumed by
// `wts()`: row-vector convention, so
//
//     clip.x = row0 . (x, y, z, 1)
//     clip.y = row1 . (x, y, z, 1)
//     clip.w = row3 . (x, y, z, 1)
//
// Rows 0, 1 and 3 match the game's world-to-clip matrix. Row 2 is a standard
// D3D depth mapping and is not used by the ESP, so it is not compared during
// calibration.
//
// Dependency-free on purpose: compiles on macOS for tests, and is mirrored by
// the Go viewer.

#include "net/protocol.hpp"

namespace net
{
	// Horizontal projection scale for a FOV.
	//
	// kFovSourceHorPlus: the game keeps a 4:3 horizontal FOV and expands
	// horizontally on wider screens (Source behaviour), so the effective
	// horizontal FOV grows with the aspect ratio.
	//
	// kFovHorizontal: the FOV is already the horizontal FOV of the current
	// aspect ratio.
	inline float XScaleFromFov(float fov_deg, float aspect, uint8_t fov_mode)
	{
		const float tan_half = std::tan(DegToRad(fov_deg) * 0.5f);
		if (tan_half <= 0.f) return 0.f;

		if (fov_mode == kFovHorizontal)
			return 1.f / tan_half;

		if (aspect <= 0.f) return 0.f;
		return (4.f / 3.f) / (aspect * tan_half);
	}

	// Recovers the horizontal scale from a real world-to-clip matrix.
	inline float MatrixXScale(const float m[16])
	{
		return std::sqrt(m[0] * m[0] + m[1] * m[1] + m[2] * m[2]);
	}

	inline float MatrixYScale(const float m[16])
	{
		return std::sqrt(m[4] * m[4] + m[5] * m[5] + m[6] * m[6]);
	}

	// Builds the world-to-clip matrix for a camera.
	inline void BuildViewMatrix(
		const Vec3& eye,
		const Vec2& angles_deg,
		float fov_deg,
		float aspect,
		uint8_t fov_mode,
		float out[16])
	{
		const float pitch = DegToRad(angles_deg.x);
		const float yaw = DegToRad(angles_deg.y);

		const float cp = std::cos(pitch);
		const float sp = std::sin(pitch);
		const float cy = std::cos(yaw);
		const float sy = std::sin(yaw);

		// Source engine basis: +X forward, +Y left, +Z up.
		const Vec3 fwd{ cp * cy, cp * sy, -sp };
		const Vec3 right{ sy, -cy, 0.f };
		const Vec3 up{ sp * cy, sp * sy, cp };

		const float xs = XScaleFromFov(fov_deg, aspect, fov_mode);
		const float ys = xs * (aspect > 0.f ? aspect : 1.f);

		const float dr = right.x * eye.x + right.y * eye.y + right.z * eye.z;
		const float du = up.x * eye.x + up.y * eye.y + up.z * eye.z;
		const float df = fwd.x * eye.x + fwd.y * eye.y + fwd.z * eye.z;

		// row 0: x
		out[0] = xs * right.x; out[1] = xs * right.y; out[2] = xs * right.z; out[3] = -xs * dr;
		// row 1: y
		out[4] = ys * up.x; out[5] = ys * up.y; out[6] = ys * up.z; out[7] = -ys * du;
		// row 2: depth (D3D style, near = 1, far = 10000); unused by the ESP
		{
			constexpr float kNear = 1.f;
			constexpr float kFar = 10000.f;
			const float a = kFar / (kFar - kNear);
			const float b = -(kFar * kNear) / (kFar - kNear);
			out[8] = a * fwd.x; out[9] = a * fwd.y; out[10] = a * fwd.z; out[11] = a * (-df) + b;
		}
		// row 3: w
		out[12] = fwd.x; out[13] = fwd.y; out[14] = fwd.z; out[15] = -df;
	}

	struct FovDetection
	{
		uint8_t mode = kFovSourceHorPlus;
		float error_hor_plus = 0.f;
		float error_horizontal = 0.f;
	};

	// Picks the FOV interpretation that reproduces the host's real matrix.
	// The host runs this once per session against its own view matrix.
	inline FovDetection DetectFovMode(float fov_deg, float aspect, const float m[16])
	{
		FovDetection d;
		const float measured = MatrixXScale(m);
		if (measured <= 0.f || fov_deg <= 0.f || aspect <= 0.f)
			return d;

		const float a = XScaleFromFov(fov_deg, aspect, kFovSourceHorPlus);
		const float b = XScaleFromFov(fov_deg, aspect, kFovHorizontal);

		d.error_hor_plus = std::fabs(a - measured) / measured;
		d.error_horizontal = std::fabs(b - measured) / measured;
		d.mode = d.error_hor_plus <= d.error_horizontal ? kFovSourceHorPlus : kFovHorizontal;
		return d;
	}

	// Same projection the game overlay uses (see view_matrix_t::wts).
	inline bool Project(const float m[16], const Vec3& p, float screen_w, float screen_h, Vec2& out)
	{
		const float w = m[12] * p.x + m[13] * p.y + m[14] * p.z + m[15];
		if (w <= 0.01f)
			return false;

		const float cx = m[0] * p.x + m[1] * p.y + m[2] * p.z + m[3];
		const float cy = m[4] * p.x + m[5] * p.y + m[6] * p.z + m[7];

		const float half_w = screen_w * 0.5f;
		const float half_h = screen_h * 0.5f;

		out.x = half_w + (cx / w) * half_w;
		out.y = half_h - (cy / w) * half_h;
		return true;
	}
}
