FROM node:22-bookworm-slim@sha256:d649c27dae7ba0137b3cef5dd75baa422c08dc3d9e3fc0c23dfb172dc3cc6436 AS build

WORKDIR /workspace/apps/web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci --ignore-scripts

COPY apps/web/ ./
COPY packages/contracts /workspace/packages/contracts
RUN npm run sync-kws-models
ARG LINGOW_API_BASE_URL=http://api:8080
ARG LINGOW_REALTIME_BASE_URL=http://realtime-audio:8090
ARG NEXT_PUBLIC_LINGOW_INITIAL_MODE=assistant
ENV LINGOW_API_BASE_URL=${LINGOW_API_BASE_URL}
ENV LINGOW_REALTIME_BASE_URL=${LINGOW_REALTIME_BASE_URL}
ENV NEXT_PUBLIC_LINGOW_INITIAL_MODE=${NEXT_PUBLIC_LINGOW_INITIAL_MODE}
RUN npm run build

FROM node:22-bookworm-slim@sha256:d649c27dae7ba0137b3cef5dd75baa422c08dc3d9e3fc0c23dfb172dc3cc6436

ENV NODE_ENV=production
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

WORKDIR /app
COPY --from=build /workspace/apps/web/public ./public
COPY --from=build /workspace/apps/web/.next/standalone ./
COPY --from=build /workspace/apps/web/.next/static ./.next/static

USER node
EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=12 CMD ["node", "-e", "fetch('http://127.0.0.1:3000/').then((response) => { if (!response.ok) process.exit(1); }).catch(() => process.exit(1))"]
CMD ["node", "server.js"]
