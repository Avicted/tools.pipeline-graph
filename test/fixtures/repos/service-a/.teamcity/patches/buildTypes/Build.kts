package patches.buildTypes

import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.ui.*

changeBuildType(RelativeId("Build")) {
    expectSteps {
    }
}
