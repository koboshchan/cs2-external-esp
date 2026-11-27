# syntax=docker/dockerfile:1

# ---- builder ---------------------------------------------------------------
# Compiles TypeScript and keeps only production dependencies.
FROM node:22-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci --no-fund --no-audit

COPY tsconfig.json ./
COPY src ./src

RUN npm run build \
 && npm prune --omit=dev

# ---- runner ----------------------------------------------------------------
FROM node:22-alpine AS runner

ENV NODE_ENV=production \
    PORT=8080 \
    BIND=0.0.0.0

WORKDIR /app

# Unprivileged user: the relay needs nothing from the host.
RUN addgroup -S relay && adduser -S -G relay -H -s /sbin/nologin relay

COPY --from=builder --chown=relay:relay /app/package.json ./package.json
COPY --from=builder --chown=relay:relay /app/node_modules ./node_modules
COPY --from=builder --chown=relay:relay /app/dist ./dist

USER relay

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||8080)+'/health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "dist/index.js"]
