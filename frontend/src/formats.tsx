import {
  IconBrain, IconBrandDebian, IconBrandDocker, IconBrandFlutter, IconBrandGit, IconBrandGolang, IconBrandNpm, IconBrandPython, IconBrandRedhat, IconBrandRust, IconCoffee, IconFiles, IconFlask, IconMountain,
} from '@tabler/icons-react'
import type { Format, RepoType } from './api'

export interface FormatInfo {
  id: Format
  label: string
  icon: typeof IconFiles
  color: string
  tagline: string
  clients: string
  types: RepoType[]
  presets: { label: string; url: string }[]
  /** Browser upload support for hosted repositories. */
  upload?: { accept?: string[]; hint: string; dir?: { label: string; placeholder: string; description?: string } }
  hostedHelp: string
  proxyHelp: string
  groupHelp?: string
}

export const FORMATS: FormatInfo[] = [
  {
    id: 'maven', label: 'Maven', icon: IconCoffee, color: 'orange',
    tagline: 'Java & JVM libraries (jar, pom, aar)', clients: 'Maven, Gradle, sbt, Ivy',
    types: ['hosted', 'proxy', 'group'],
    presets: [
      { label: 'Maven Central', url: 'https://repo1.maven.org/maven2' },
      { label: 'Google Maven (Android)', url: 'https://maven.google.com' },
      { label: 'Gradle Plugin Portal', url: 'https://plugins.gradle.org/m2' },
      { label: 'Apache Snapshots', url: 'https://repository.apache.org/snapshots' },
    ],
    upload: { hint: 'Upload jars, poms and checksums into a Maven layout path.', dir: { label: 'Directory', placeholder: 'com/acme/library/1.0.0', description: 'groupId as folders / artifactId / version' } },
    hostedHelp: 'Publish your own releases and snapshots with mvn deploy or Gradle publish.',
    proxyHelp: 'Cache Maven Central or another remote so builds are fast and keep working when it is down.',
    groupHelp: 'Give builds one URL that searches your releases, snapshots and Maven Central.',
  },
  {
    id: 'npm', label: 'npm', icon: IconBrandNpm, color: 'red',
    tagline: 'JavaScript & TypeScript packages', clients: 'npm, Yarn, pnpm, Bun',
    types: ['hosted', 'proxy', 'group'],
    presets: [
      { label: 'npmjs.org', url: 'https://registry.npmjs.org' },
      { label: 'Yarn registry', url: 'https://registry.yarnpkg.com' },
      { label: 'GitHub Packages', url: 'https://npm.pkg.github.com' },
    ],
    hostedHelp: 'Publish private packages with npm publish.',
    proxyHelp: 'Cache the public npm registry for fast, reliable installs.',
    groupHelp: 'One registry URL for private and public packages — point .npmrc here.',
  },
  {
    id: 'pypi', label: 'PyPI', icon: IconBrandPython, color: 'blue',
    tagline: 'Python wheels & source distributions', clients: 'pip, uv, Poetry, twine',
    types: ['hosted', 'proxy', 'group'],
    presets: [
      { label: 'PyPI', url: 'https://pypi.org' },
      { label: 'TestPyPI', url: 'https://test.pypi.org' },
    ],
    hostedHelp: 'Upload private packages with twine or poetry publish.',
    proxyHelp: 'Cache pypi.org; packages are downloaded once and served locally afterwards.',
    groupHelp: 'One index URL for pip that merges private and public packages.',
  },
  {
    id: 'docker', label: 'Docker', icon: IconBrandDocker, color: 'cyan',
    tagline: 'OCI & Docker container images', clients: 'Docker, Podman, containerd, Kubernetes',
    types: ['hosted', 'proxy', 'group'],
    presets: [
      { label: 'Docker Hub', url: 'https://registry-1.docker.io' },
      { label: 'GitHub Container Registry', url: 'https://ghcr.io' },
      { label: 'Quay.io', url: 'https://quay.io' },
      { label: 'Kubernetes registry', url: 'https://registry.k8s.io' },
      { label: 'Microsoft (MCR)', url: 'https://mcr.microsoft.com' },
      { label: 'AWS Public ECR', url: 'https://public.ecr.aws' },
    ],
    hostedHelp: 'Push your own images with docker push. Layers are de-duplicated across images.',
    proxyHelp: 'Pull-through cache for Docker Hub and other registries — avoids rate limits.',
    groupHelp: 'Pull from several registries through one name (pull only; push to a hosted repository).',
  },
  {
    id: 'go', label: 'Go modules', icon: IconBrandGolang, color: 'teal',
    tagline: 'Go module proxy (GOPROXY)', clients: 'go command',
    types: ['hosted', 'proxy', 'group'],
    presets: [
      { label: 'proxy.golang.org', url: 'https://proxy.golang.org' },
      { label: 'goproxy.io', url: 'https://goproxy.io' },
      { label: 'goproxy.cn', url: 'https://goproxy.cn' },
    ],
    upload: { accept: ['.mod', '.zip', '.info'], hint: 'Upload <version>.mod and <version>.zip (made with git archive). The .info file is created for you.', dir: { label: 'Module directory', placeholder: 'example.com/team/module/@v', description: 'Your module path followed by /@v' } },
    hostedHelp: 'Serve private modules without giving builds access to your git server.',
    proxyHelp: 'Cache proxy.golang.org so builds are reproducible even if a module disappears upstream.',
    groupHelp: 'Set GOPROXY to one URL that resolves private and public modules.',
  },
  {
    id: 'apt', label: 'APT', icon: IconBrandDebian, color: 'pink',
    tagline: 'Debian & Ubuntu packages (.deb)', clients: 'apt, apt-get',
    types: ['hosted', 'proxy'],
    presets: [
      { label: 'Debian', url: 'http://deb.debian.org/debian' },
      { label: 'Debian security', url: 'http://security.debian.org/debian-security' },
      { label: 'Ubuntu', url: 'http://archive.ubuntu.com/ubuntu' },
      { label: 'Ubuntu ports (arm64)', url: 'http://ports.ubuntu.com/ubuntu-ports' },
    ],
    upload: { accept: ['.deb'], hint: 'Drop .deb packages. They are placed in pool/ and the signed index is rebuilt automatically.' },
    hostedHelp: 'Distribute your own .deb packages. Indexes are generated and GPG-signed automatically.',
    proxyHelp: 'Mirror Debian or Ubuntu on demand — only packages you install are downloaded.',
  },
  {
    id: 'yum', label: 'YUM / DNF', icon: IconBrandRedhat, color: 'grape',
    tagline: 'RHEL, Rocky, Alma & Fedora packages (.rpm)', clients: 'dnf, yum, zypper',
    types: ['hosted', 'proxy'],
    presets: [
      { label: 'Rocky Linux 9 BaseOS', url: 'https://dl.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os' },
      { label: 'Rocky Linux 9 AppStream', url: 'https://dl.rockylinux.org/pub/rocky/9/AppStream/x86_64/os' },
      { label: 'AlmaLinux 9 BaseOS', url: 'https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os' },
      { label: 'EPEL 9', url: 'https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64' },
    ],
    upload: { accept: ['.rpm'], hint: 'Drop .rpm packages. repodata/ is regenerated and signed automatically.', dir: { label: 'Directory (optional)', placeholder: 'Packages', description: 'Leave empty to file packages under Packages/<letter>/' } },
    hostedHelp: 'Distribute your own RPMs. repodata is generated and GPG-signed automatically.',
    proxyHelp: 'Cache a distribution repository (use a baseurl, not a mirrorlist).',
  },
  {
    id: 'conda', label: 'Conda', icon: IconFlask, color: 'green',
    tagline: 'Anaconda & conda-forge packages (.conda, .tar.bz2)', clients: 'conda, mamba, micromamba, pixi',
    types: ['hosted', 'proxy'],
    presets: [
      { label: 'conda-forge', url: 'https://conda.anaconda.org/conda-forge' },
      { label: 'bioconda', url: 'https://conda.anaconda.org/bioconda' },
      { label: 'Anaconda main (license terms apply)', url: 'https://repo.anaconda.com/pkgs/main' },
      { label: 'Anaconda r', url: 'https://repo.anaconda.com/pkgs/r' },
    ],
    upload: { accept: ['.conda', '.bz2'], hint: 'Drop .conda or .tar.bz2 packages. The file name, platform folder (noarch, linux-64…) and channel index come from the package itself.' },
    hostedHelp: 'Publish your own conda packages; repodata.json is generated automatically.',
    proxyHelp: 'Cache a conda channel such as conda-forge. Only packages you install are downloaded.',
  },
  {
    id: 'cargo', label: 'Rust / Cargo', icon: IconBrandRust, color: 'orange',
    tagline: 'Rust crates (sparse registry)', clients: 'cargo',
    types: ['hosted', 'proxy', 'group'],
    presets: [{ label: 'crates.io', url: 'https://index.crates.io' }],
    hostedHelp: 'Publish private crates with cargo publish. The index is generated for you.',
    proxyHelp: 'Cache crates.io so CI builds are fast and keep working when it is down.',
    groupHelp: 'One registry for your crates and crates.io, merged per crate.',
  },
  {
    id: 'pub', label: 'Dart / Flutter (pub)', icon: IconBrandFlutter, color: 'blue',
    tagline: 'Dart and Flutter packages', clients: 'dart pub, flutter pub',
    types: ['hosted', 'proxy', 'group'],
    presets: [{ label: 'pub.dev', url: 'https://pub.dev' }],
    hostedHelp: 'Publish private packages with dart pub publish.',
    proxyHelp: 'Cache pub.dev; archives are downloaded once and served locally afterwards.',
    groupHelp: 'One hosted URL for private and public packages.',
  },
  {
    id: 'huggingface', label: 'Hugging Face', icon: IconBrain, color: 'yellow',
    tagline: 'Models, datasets and spaces', clients: 'huggingface_hub, transformers, huggingface-cli',
    types: ['hosted', 'proxy', 'group'],
    presets: [{ label: 'huggingface.co', url: 'https://huggingface.co' }],
    upload: { hint: 'Upload model files into <org>/<model>/resolve/main. Use datasets/<org>/<name>/resolve/main for datasets.', dir: { label: 'Directory', placeholder: 'acme/my-model/resolve/main', description: 'Model id, then /resolve/main' } },
    hostedHelp: 'Serve your own models and datasets to huggingface_hub (set HF_ENDPOINT). Files are uploaded with curl or the Browse tab.',
    proxyHelp: 'Cache public models and datasets so large downloads happen once. Public repositories only.',
    groupHelp: 'Look up models in your hosted repository first, then in the cache.',
  },
  {
    id: 'lfs', label: 'Git LFS', icon: IconBrandGit, color: 'red',
    tagline: 'Large file storage for git', clients: 'git lfs',
    types: ['hosted'],
    presets: [],
    hostedHelp: 'Store large files outside your git server. Identical files are stored once.',
    proxyHelp: '',
  },
  {
    id: 'alpine', label: 'Alpine', icon: IconMountain, color: 'indigo',
    tagline: 'Alpine Linux packages (.apk)', clients: 'apk',
    types: ['hosted', 'proxy'],
    presets: [
      { label: 'Alpine (dl-cdn)', url: 'https://dl-cdn.alpinelinux.org/alpine' },
      { label: 'Alpine (kernel.org mirror)', url: 'https://mirrors.edge.kernel.org/alpine' },
    ],
    upload: { accept: ['.apk'], hint: 'Drop .apk packages into a folder such as v3.20/main/x86_64. APKINDEX.tar.gz is generated for that folder.', dir: { label: 'Directory', placeholder: 'v3.20/main/x86_64' } },
    hostedHelp: 'Distribute your own .apk packages. The index is generated (unsigned, so clients use --allow-untrusted).',
    proxyHelp: 'Mirror an Alpine mirror on demand. Only packages you install are downloaded.',
  },
  {
    id: 'raw', label: 'Raw', icon: IconFiles, color: 'gray',
    tagline: 'Any file: binaries, archives, installers, docs', clients: 'curl, wget, browsers',
    types: ['hosted', 'proxy', 'group'],
    presets: [
      { label: 'GitHub (releases)', url: 'https://github.com' },
      { label: 'Node.js downloads', url: 'https://nodejs.org/dist' },
    ],
    upload: { hint: 'Upload any files into a folder of your choice.', dir: { label: 'Directory', placeholder: 'releases/1.0' } },
    hostedHelp: 'Store build outputs, installers and other files over plain HTTP.',
    proxyHelp: 'Cache downloads from any website (e.g. GitHub release assets).',
    groupHelp: 'Search several raw repositories through one URL.',
  },
]

export const formatInfo = (f: Format) => FORMATS.find((x) => x.id === f)!

export const TYPES: Record<RepoType, { label: string; summary: string; detail: string }> = {
  hosted: {
    label: 'Hosted', summary: 'Store your own artifacts',
    detail: 'Your team publishes artifacts here. This is the source of truth for things you build.',
  },
  proxy: {
    label: 'Proxy', summary: 'Cache a remote repository',
    detail: 'Fetches from a remote on first request and keeps a copy. Builds get faster and survive remote outages.',
  },
  group: {
    label: 'Group', summary: 'Combine repositories behind one URL',
    detail: 'Searches member repositories in order. Clients configure a single URL for everything.',
  },
}

export function FormatIcon({ format, size = 18 }: { format: Format; size?: number }) {
  const f = formatInfo(format)
  return <f.icon size={size} color={`var(--mantine-color-${f.color}-6)`} stroke={1.7} />
}
