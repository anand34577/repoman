import type { Repo } from './api'

export interface Step { title: string; description?: string; file?: string; code: string }
export interface ClientGuide { id: string; label: string; steps: Step[] }

export const repoUrl = (base: string, r: Repo) =>
  r.format === 'docker' ? `${new URL(base).host}/${r.name}` : `${base}/repository/${r.name}/`

/**
 * Step-by-step client configuration for a repository. `user` and `token` are filled into
 * snippets so people can copy them verbatim.
 */
export function clientGuides(base: string, r: Repo, user: string, token: string): ClientGuide[] {
  const url = repoUrl(base, r)
  const host = new URL(base).host
  const hostPath = url.replace(/^https?:\/\//, '')
  const authUrl = url.replace(/^(https?:\/\/)/, `$1${encodeURIComponent(user)}:${token}@`)
  const deploy = r.type === 'hosted'
  const id = `repoman-${r.name}`

  switch (r.format) {
    case 'maven': {
      const server = `<server>
  <id>${id}</id>
  <username>${user}</username>
  <password>${token}</password>
</server>`
      return [
        {
          id: 'maven', label: 'Maven', steps: deploy ? [
            { title: 'Add your credentials', file: '~/.m2/settings.xml', description: 'Inside <servers>. The id links credentials to the repository below.', code: server },
            { title: 'Point distribution at this repository', file: 'pom.xml', code: `<distributionManagement>\n  <repository>\n    <id>${id}</id>\n    <url>${url}</url>\n  </repository>\n  <snapshotRepository>\n    <id>${id}</id>\n    <url>${url}</url>\n  </snapshotRepository>\n</distributionManagement>` },
            { title: 'Deploy', code: 'mvn deploy' },
          ] : [
            { title: 'Route all downloads through RepoMan', file: '~/.m2/settings.xml', description: 'The mirror sends every dependency request here; the server block holds credentials.', code: `<settings>\n  <mirrors>\n    <mirror>\n      <id>${id}</id>\n      <mirrorOf>*</mirrorOf>\n      <url>${url}</url>\n    </mirror>\n  </mirrors>\n  <servers>\n${server.replace(/^/gm, '    ')}\n  </servers>\n</settings>` },
            { title: 'Build as usual', code: 'mvn -U clean verify' },
          ],
        },
        {
          id: 'gradle-kts', label: 'Gradle (Kotlin)', steps: [
            { title: deploy ? 'Publish to this repository' : 'Resolve dependencies from this repository', file: 'build.gradle.kts', code: deploy
              ? `plugins { \`maven-publish\` }\n\npublishing {\n  repositories {\n    maven {\n      url = uri("${url}")\n      credentials {\n        username = "${user}"\n        password = "${token}"\n      }\n    }\n  }\n}`
              : `repositories {\n  maven {\n    url = uri("${url}")\n    credentials {\n      username = "${user}"\n      password = "${token}"\n    }\n  }\n}` },
            ...(deploy ? [{ title: 'Publish', code: './gradlew publish' }] : []),
          ],
        },
        {
          id: 'gradle', label: 'Gradle (Groovy)', steps: [
            { title: deploy ? 'Publish to this repository' : 'Resolve dependencies from this repository', file: 'build.gradle', code: deploy
              ? `apply plugin: 'maven-publish'\n\npublishing {\n  repositories {\n    maven {\n      url '${url}'\n      credentials {\n        username '${user}'\n        password '${token}'\n      }\n    }\n  }\n}`
              : `repositories {\n  maven {\n    url '${url}'\n    credentials {\n      username '${user}'\n      password '${token}'\n    }\n  }\n}` },
          ],
        },
      ]
    }
    case 'npm': {
      const npmrc = `registry=${url}\n//${hostPath}:_authToken=${token}`
      return [
        {
          id: 'npm', label: 'npm', steps: [
            { title: 'Configure the registry', file: '.npmrc (project or ~/.npmrc)', description: 'To use RepoMan only for one scope instead, replace the first line with @your-scope:registry=…', code: npmrc },
            deploy
              ? { title: 'Publish', description: 'Or sign in interactively: npm login --auth-type=legacy --registry=' + url, code: `npm publish --registry=${url}` }
              : { title: 'Install packages', code: 'npm install' },
          ],
        },
        { id: 'pnpm', label: 'pnpm', steps: [{ title: 'Configure the registry', file: '.npmrc', code: npmrc }, { title: deploy ? 'Publish' : 'Install', code: deploy ? 'pnpm publish' : 'pnpm install' }] },
        {
          id: 'yarn', label: 'Yarn (Berry)', steps: [
            { title: 'Configure the registry', file: '.yarnrc.yml', code: `npmRegistryServer: "${url}"\nnpmAlwaysAuth: true\nnpmAuthToken: "${token}"${url.startsWith('http:') ? '\nunsafeHttpWhitelist:\n  - "' + host.split(':')[0] + '"' : ''}` },
            { title: deploy ? 'Publish' : 'Install', code: deploy ? 'yarn npm publish' : 'yarn install' },
          ],
        },
      ]
    }
    case 'pypi': {
      const guides: ClientGuide[] = [
        {
          id: 'pip', label: 'pip', steps: [
            { title: 'Set the index', file: 'pip.conf (Linux/macOS: ~/.config/pip/pip.conf, Windows: %APPDATA%\\pip\\pip.ini)', code: `[global]\nindex-url = ${authUrl}simple/` },
            { title: 'Or for a single command', code: `pip install --index-url ${authUrl}simple/ requests` },
          ],
        },
        {
          id: 'uv', label: 'uv', steps: [
            { title: 'Add the index', file: 'pyproject.toml', code: `[[tool.uv.index]]\nname = "repoman"\nurl = "${url}simple/"\ndefault = true` },
            { title: 'Provide credentials', code: `export UV_INDEX_REPOMAN_USERNAME=${user}\nexport UV_INDEX_REPOMAN_PASSWORD=${token}` },
          ],
        },
        {
          id: 'poetry', label: 'Poetry', steps: [
            { title: 'Add the source and credentials', code: `poetry source add --priority=primary repoman ${url}simple/\npoetry config http-basic.repoman ${user} ${token}` },
            ...(deploy ? [{ title: 'Publish', code: `poetry config repositories.repoman-upload ${url}\npoetry config http-basic.repoman-upload ${user} ${token}\npoetry publish --build -r repoman-upload` }] : []),
          ],
        },
      ]
      if (deploy) guides.push({
        id: 'twine', label: 'twine', steps: [
          { title: 'Configure the upload target', file: '~/.pypirc', code: `[distutils]\nindex-servers = repoman\n\n[repoman]\nrepository = ${url}\nusername = ${user}\npassword = ${token}` },
          { title: 'Build and upload', code: 'python -m build\ntwine upload -r repoman dist/*' },
        ],
      })
      return guides
    }
    case 'docker': {
      const image = r.type === 'proxy' && r.remoteUrl?.includes('docker.io') ? 'library/nginx:latest' : 'myapp:1.0'
      const insecure = base.startsWith('http:') && !host.startsWith('localhost')
      return [
        {
          id: 'docker', label: 'Docker', steps: [
            ...(insecure ? [{ title: 'Allow plain HTTP (or serve RepoMan behind TLS)', file: '/etc/docker/daemon.json', description: 'Docker requires HTTPS for every registry except localhost. Restart Docker afterwards.', code: `{ "insecure-registries": ["${host}"] }` }] : []),
            { title: 'Sign in', description: 'Use an API token as the password.', code: `echo ${token} | docker login ${host} -u ${user} --password-stdin` },
            ...(deploy ? [{ title: 'Tag and push an image', code: `docker tag myapp:1.0 ${url}/myapp:1.0\ndocker push ${url}/myapp:1.0` }] : []),
            { title: 'Pull', description: 'The first path segment after the host is always the repository name.', code: `docker pull ${url}/${image}` },
          ],
        },
        {
          id: 'podman', label: 'Podman', steps: [
            { title: 'Sign in', code: `podman login ${insecure ? '--tls-verify=false ' : ''}${host} -u ${user} -p ${token}` },
            { title: deploy ? 'Push and pull' : 'Pull', code: `${deploy ? `podman push ${url}/myapp:1.0\n` : ''}podman pull ${url}/${image}` },
          ],
        },
        {
          id: 'k8s', label: 'Kubernetes', steps: [
            { title: 'Create a pull secret', code: `kubectl create secret docker-registry repoman \\\n  --docker-server=${host} \\\n  --docker-username=${user} \\\n  --docker-password=${token}` },
            { title: 'Reference it from your pods', file: 'deployment.yaml', code: `spec:\n  imagePullSecrets:\n    - name: repoman\n  containers:\n    - name: app\n      image: ${url}/${image}` },
          ],
        },
      ]
    }
    case 'go':
      return [
        {
          id: 'go', label: 'go', steps: [
            { title: 'Use this repository as your module proxy', code: `go env -w GOPROXY=${url},direct` },
            { title: 'Skip the public checksum database for private modules', description: 'Public modules are verified through this repository (it relays sum.golang.org). Modules you publish here are not in that database; list their path prefixes.', code: 'go env -w GONOSUMDB=example.com/yourcompany' },
            { title: 'Provide credentials (when anonymous access is off)', file: '~/.netrc (Windows: %USERPROFILE%\\_netrc)', code: `machine ${host.split(':')[0]}\nlogin ${user}\npassword ${token}` },
          ],
        },
        ...(deploy ? [{
          id: 'publish', label: 'Publish a module', steps: [
            { title: 'Tag a release in your module repository', code: 'git tag v1.2.3' },
            { title: 'Create the module zip and upload it with go.mod', description: 'Module path "example.com/team/mod" — use the case-escaped form (e.g. !azure) for upper-case letters.', code: `MOD=example.com/team/mod VER=v1.2.3\ngit archive --format=zip --prefix=$MOD@$VER/ -o $VER.zip $VER\ncurl -u ${user}:${token} --upload-file go.mod ${url}$MOD/@v/$VER.mod\ncurl -u ${user}:${token} --upload-file $VER.zip ${url}$MOD/@v/$VER.zip` },
          ],
        }] : []),
      ]
    case 'apt': {
      const keyring = `/etc/apt/keyrings/${id}.gpg`
      if (r.type === 'proxy') return [{
        id: 'apt', label: 'apt', steps: [
          { title: 'Replace the mirror URL', file: `/etc/apt/sources.list.d/${id}.list`, description: 'Packages keep their upstream signatures, so the distribution keyring already trusts them. Use your release codename.', code: `deb ${url} bookworm main` },
          { title: 'Update', code: 'sudo apt update' },
        ],
      }]
      return [
        {
          id: 'apt', label: 'apt', steps: [
            { title: 'Trust the RepoMan signing key', code: `sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL ${url}repository.key | sudo gpg --dearmor -o ${keyring}` },
            { title: 'Add the repository', code: `echo "deb [signed-by=${keyring}] ${url} ${r.distribution || 'stable'} main" | sudo tee /etc/apt/sources.list.d/${id}.list` },
            { title: 'Credentials (when anonymous access is off)', file: `/etc/apt/auth.conf.d/${id}.conf`, code: `machine ${hostPath}\nlogin ${user}\npassword ${token}` },
            { title: 'Install', code: 'sudo apt update && sudo apt install <package>' },
          ],
        },
        { id: 'publish', label: 'Upload packages', steps: [{ title: 'Upload a .deb', description: 'Or drag and drop files on the Browse tab.', code: `curl -u ${user}:${token} --upload-file ./mypackage_1.0_amd64.deb ${url}` }] },
      ]
    }
    case 'yum': {
      const auth = `\nusername=${user}\npassword=${token}`
      if (r.type === 'proxy') return [{
        id: 'dnf', label: 'dnf / yum', steps: [
          { title: 'Add the repository', file: `/etc/yum.repos.d/${id}.repo`, description: 'Packages keep their upstream signatures; point gpgkey at your distribution key.', code: `[${id}]\nname=${r.name}\nbaseurl=${url}\nenabled=1\ngpgcheck=1\ngpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-Rocky-9${auth}` },
          { title: 'Install', code: 'sudo dnf makecache && sudo dnf install <package>' },
        ],
      }]
      return [
        {
          id: 'dnf', label: 'dnf / yum', steps: [
            { title: 'Add the repository', file: `/etc/yum.repos.d/${id}.repo`, description: 'repo_gpgcheck verifies the signed metadata with the RepoMan key.', code: `[${id}]\nname=${r.name}\nbaseurl=${url}\nenabled=1\ngpgcheck=0\nrepo_gpgcheck=1\ngpgkey=${url}repository.key${auth}` },
            { title: 'Install', code: 'sudo dnf makecache && sudo dnf install <package>' },
          ],
        },
        { id: 'publish', label: 'Upload packages', steps: [{ title: 'Upload an .rpm', description: 'Or drag and drop files on the Browse tab.', code: `curl -u ${user}:${token} --upload-file ./mypackage-1.0-1.x86_64.rpm ${url}` }] },
      ]
    }
    case 'conda': {
      const channel = authUrl.replace(/\/$/, '')
      const plain = url.replace(/\/$/, '')
      return [
        {
          id: 'conda', label: 'conda / mamba', steps: [
            { title: 'Use only this channel', file: '~/.condarc (Windows: %USERPROFILE%\\.condarc; micromamba also reads it)', description: 'Listing it alone keeps conda from contacting repo.anaconda.com. Add more RepoMan channels below it to search them in order. Drop the user:token part if anonymous access is on.', code: `channels:\n  - ${channel}\nchannel_priority: strict` },
            { title: 'Or for a single command', code: `conda install --override-channels -c ${channel} numpy` },
          ],
        },
        {
          id: 'pixi', label: 'pixi', steps: [
            { title: 'Add the channel', file: 'pixi.toml', code: `[workspace]\nchannels = ["${plain}"]` },
            { title: 'Provide credentials (when anonymous access is off)', code: `pixi auth login ${host} --username ${user} --password ${token}` },
          ],
        },
        ...(deploy ? [{
          id: 'publish', label: 'Upload packages', steps: [
            { title: 'Upload a .conda or .tar.bz2', description: 'The platform folder comes from the package. Or drag and drop files on the Browse tab.', code: `curl -u ${user}:${token} --upload-file ./mypkg-1.0-h1234_0.conda ${url}` },
            { title: 'Build and upload everything', code: `conda build recipe/ --output-folder ./out\nfor f in ./out/*/*.conda ./out/*/*.tar.bz2; do\n  [ -e "$f" ] && curl -fu ${user}:${token} --upload-file "$f" ${url}\ndone` },
          ],
        }] : []),
      ]
    }
    default:
      return [
        {
          id: 'curl', label: 'curl', steps: [
            { title: 'Download', code: `curl -fLO -u ${user}:${token} ${url}path/to/file.zip` },
            ...(deploy ? [{ title: 'Upload', code: `curl -u ${user}:${token} --upload-file ./file.zip ${url}path/to/file.zip` }] : []),
          ],
        },
        { id: 'wget', label: 'wget', steps: [{ title: 'Download', code: `wget --user=${user} --password=${token} ${url}path/to/file.zip` }] },
      ]
  }
}
